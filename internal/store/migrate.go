package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// OpenRaw 打开数据库但**不执行迁移**。跨库搬运时用它打开源库，保证源库只被 SELECT、
// 不建表、不加列、不改结构、不删除任何行。
func OpenRaw(ctx context.Context, dialect Dialect, dsn string, maxOpenConns int) (*DB, error) {
	sqlDB, err := sql.Open(dialect.DriverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if maxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(maxOpenConns)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	return &DB{SQL: sqlDB, dialect: dialect}, nil
}

// MigrateOptions 控制跨库搬运行为。
type MigrateOptions struct {
	// DryRun 只统计与校验，不写目标库。
	DryRun bool
	// Verify 搬运后逐表比对条数与抽样行。
	Verify bool
	// SampleSize 抽样比对行数（默认 5）。
	SampleSize int
}

// TableReport 是单表搬运结果。
type TableReport struct {
	Table      string
	Source     int64
	Written    int64
	Dest       int64
	SampleRows int
	Mismatches []string
	VerifiedOK bool
}

// MigrateReport 是整轮搬运结果。
type MigrateReport struct {
	DryRun bool
	Verify bool
	Tables []TableReport
}

// OK 表示整轮无条数不符、无抽样差异（仅在 Verify 时有意义）。
func (r *MigrateReport) OK() bool {
	for _, t := range r.Tables {
		if len(t.Mismatches) > 0 {
			return false
		}
	}
	return true
}

// MigrateData 把源库逐表拷贝到目标库：拷贝不是清洗，保留原始值（数值列归一为 float64）。
// 按 CopyTables 顺序逐表；每表单事务 upsert，天然幂等。源库只读（仅 SELECT）。
func MigrateData(ctx context.Context, src, dst *DB, opts MigrateOptions) (*MigrateReport, error) {
	if opts.SampleSize <= 0 {
		opts.SampleSize = 5
	}
	report := &MigrateReport{DryRun: opts.DryRun, Verify: opts.Verify}

	var conn *sql.Conn
	if !opts.DryRun {
		c, err := dst.SQL.Conn(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取目标库连接失败: %w", err)
		}
		defer c.Close()
		if err := dst.dialect.PrepareCopy(ctx, c); err != nil {
			return nil, err
		}
		conn = c
	}

	for _, table := range CopyTables {
		cols := CopyColumns(table)
		if len(cols) == 0 {
			return nil, fmt.Errorf("未知表 %s", table)
		}
		rows, err := readTable(ctx, src, table, cols)
		if err != nil {
			return nil, err
		}
		tr := TableReport{Table: table, Source: int64(len(rows))}
		if !opts.DryRun {
			if err := writeTable(ctx, conn, dst.dialect, table, cols, rows); err != nil {
				return nil, err
			}
			tr.Written = int64(len(rows))
		}
		if opts.Verify {
			if err := verifyTable(ctx, src, dst, table, cols, rows, opts.SampleSize, &tr); err != nil {
				return nil, err
			}
		}
		report.Tables = append(report.Tables, tr)
	}
	return report, nil
}

// readTable 读出整表（仅 SELECT），并把每行的值归一为两库都能安全写入的形态。
func readTable(ctx context.Context, db *DB, table string, cols []string) ([][]any, error) {
	rows, err := db.SQL.QueryContext(ctx, "SELECT "+strings.Join(cols, ", ")+" FROM "+table)
	if err != nil {
		return nil, fmt.Errorf("读取源表 %s 失败: %w", table, err)
	}
	defer rows.Close()
	out := make([][]any, 0)
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("扫描源表 %s 失败: %w", table, err)
		}
		out = append(out, normalizeRow(table, cols, raw))
	}
	return out, rows.Err()
}

// normalizeRow 把驱动返回的裸值转成两库都能安全写入的形态：数值列 → float64，字节串 → string。
func normalizeRow(table string, cols []string, raw []any) []any {
	out := make([]any, len(cols))
	for i, col := range cols {
		if IsFloatColumn(table, col) && raw[i] != nil {
			out[i] = toFloat(raw[i])
			continue
		}
		switch v := raw[i].(type) {
		case []byte:
			out[i] = string(v)
		default:
			out[i] = v
		}
	}
	return out
}

// writeTable 在固定连接上开单表事务，逐行 upsert（幂等）。
func writeTable(ctx context.Context, conn *sql.Conn, dialect Dialect, table string, cols []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	stmt := dialect.UpsertSQL(table, cols, KeyColumns(table))
	placeholders := make([]any, len(cols))
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启目标表 %s 事务失败: %w", table, err)
	}
	for _, row := range rows {
		copy(placeholders, row)
		if _, err := tx.ExecContext(ctx, stmt, placeholders...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("写入目标表 %s 失败: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目标表 %s 事务失败: %w", table, err)
	}
	return nil
}

// verifyTable 比对条数与抽样行；差异写进 tr.Mismatches。
func verifyTable(ctx context.Context, src, dst *DB, table string, cols []string, rows [][]any, sampleSize int, tr *TableReport) error {
	dstCount, err := countTableRows(ctx, dst, table)
	if err != nil {
		return err
	}
	tr.Dest = dstCount
	if dstCount != tr.Source {
		tr.Mismatches = append(tr.Mismatches,
			fmt.Sprintf("条数不一致: 源 %d / 目标 %d", tr.Source, dstCount))
	}
	keys := KeyColumns(table)
	sampled := 0
	for _, row := range rows {
		if sampled >= sampleSize {
			break
		}
		sampled++
		destRow, err := readByKey(ctx, dst, table, cols, keys, row)
		if err != nil {
			return err
		}
		if destRow == nil {
			tr.Mismatches = append(tr.Mismatches, fmt.Sprintf("抽样缺行: %s=%v", keys[0], row[keyIndex(cols, keys[0])]))
			continue
		}
		if !rowsEqual(row, destRow) {
			tr.Mismatches = append(tr.Mismatches, fmt.Sprintf("抽样不一致: %s=%v", keys[0], row[keyIndex(cols, keys[0])]))
		}
	}
	tr.SampleRows = sampled
	tr.VerifiedOK = len(tr.Mismatches) == 0
	return nil
}

// countTableRows 统计目标表行数。
func countTableRows(ctx context.Context, db *DB, table string) (int64, error) {
	var n int64
	if err := db.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计目标表 %s 失败: %w", table, err)
	}
	return n, nil
}

// readByKey 按键列从目标表读回一行（用 srcRow 的键值定位）。
func readByKey(ctx context.Context, db *DB, table string, cols, keys []string, srcRow []any) ([]any, error) {
	keyVals := make([]any, len(keys))
	for i, k := range keys {
		keyVals[i] = srcRow[keyIndex(cols, k)]
	}
	where := make([]string, len(keys))
	for i, k := range keys {
		where[i] = k + " = ?"
	}
	query := "SELECT " + strings.Join(cols, ", ") + " FROM " + table + " WHERE " + strings.Join(where, " AND ")
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	err := db.SQL.QueryRowContext(ctx, query, keyVals...).Scan(ptrs...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("抽样读取目标表 %s 失败: %w", table, err)
	}
	return normalizeRow(table, cols, raw), nil
}

func keyIndex(cols []string, key string) int {
	for i, c := range cols {
		if c == key {
			return i
		}
	}
	return -1
}

// rowsEqual 比较两行的列值（数字先归一为 float64，其余转字符串）。
func rowsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(normalizeScalar(a[i]), normalizeScalar(b[i])) {
			return false
		}
	}
	return true
}

// normalizeScalar 把数值型的各种表达归一为 float64，其余转 string，消除驱动差异。
func normalizeScalar(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case float64:
		return t
	case []byte:
		if f, ok := parseNumeric(string(t)); ok {
			return f
		}
		return string(t)
	case string:
		if f, ok := parseNumeric(t); ok {
			return f
		}
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

// toFloat 把驱动返回的数值表达归一为 float64。
func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case []byte:
		f, _ := strconv.ParseFloat(string(t), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

// parseNumeric 尝试把字符串解析为浮点；用于抽样比对时消除「数值列以文本返回」的差异。
func parseNumeric(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
