package main

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// mergeReport 是单表搬运与校验结果。
type mergeReport struct {
	table      string
	source     int64
	written    int64
	dest       int64
	sampleRows int
	mismatches []string
}

// migrateAll 逐表搬运并校验：源计数 → 拷贝 → 目标计数 → 抽样比对。
func migrateAll(ctx context.Context, src, dst *sql.DB, sampleSize int) error {
	fmt.Println("旧 SQLite → MySQL 一次性搬运")
	var totalSrc, totalWritten int64
	failed := false
	for _, spec := range tables {
		rep := mergeReport{table: spec.name}
		rows, err := readTable(ctx, src, spec)
		if err != nil {
			return err
		}
		rep.source = int64(len(rows))
		if err := writeTable(ctx, dst, spec, rows); err != nil {
			return err
		}
		rep.written = int64(len(rows))
		if err := verifyTable(ctx, dst, spec, rows, sampleSize, &rep); err != nil {
			return err
		}
		printTableReport(rep)
		totalSrc += rep.source
		totalWritten += rep.written
		if len(rep.mismatches) > 0 {
			failed = true
		}
	}
	fmt.Printf("合计：源 %d 行，写入 %d 行\n", totalSrc, totalWritten)
	if failed {
		return fmt.Errorf("存在条数或抽样不一致，请核对上方明细")
	}
	fmt.Println("逐表条数与抽样比对通过。")
	return nil
}

// reportDryRun 只读源库报告每表行数，不连目标库、不写任何数据。
func reportDryRun(ctx context.Context, src *sql.DB, sqlitePath string) error {
	fmt.Printf("演练模式（dry-run）：只读源库 %s，不写目标库\n", sqlitePath)
	var total int64
	for _, spec := range tables {
		n, err := countRows(ctx, src, spec.name)
		if err != nil {
			return err
		}
		fmt.Printf("  %-16s 源 %d 行\n", spec.name, n)
		total += n
	}
	fmt.Printf("  合计 %d 行，将从旧 SQLite 搬到 MySQL\n", total)
	return nil
}

// readTable 读出整表（仅 SELECT），并把每行的值归一为可安全写入 MySQL 的形态。
func readTable(ctx context.Context, db *sql.DB, spec tableSpec) ([][]any, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+strings.Join(spec.columns, ", ")+" FROM "+spec.name)
	if err != nil {
		return nil, fmt.Errorf("读取源表 %s 失败: %w", spec.name, err)
	}
	defer rows.Close()
	out := make([][]any, 0)
	for rows.Next() {
		raw := make([]any, len(spec.columns))
		ptrs := make([]any, len(spec.columns))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("扫描源表 %s 失败: %w", spec.name, err)
		}
		out = append(out, normalizeRow(spec, raw))
	}
	return out, rows.Err()
}

// normalizeRow 把驱动返回的裸值转成可写形态：数值列 → float64，[]byte → string。
func normalizeRow(spec tableSpec, raw []any) []any {
	out := make([]any, len(spec.columns))
	for i, col := range spec.columns {
		if spec.floatCols[col] && raw[i] != nil {
			out[i] = toFloat(raw[i])
			continue
		}
		if b, ok := raw[i].([]byte); ok {
			out[i] = string(b)
			continue
		}
		out[i] = raw[i]
	}
	return out
}

// upsertSQL 生成 MySQL 形态的幂等写入（ON DUPLICATE KEY UPDATE）。
func upsertSQL(spec tableSpec) string {
	placeholders := make([]string, len(spec.columns))
	for i := range spec.columns {
		placeholders[i] = "?"
	}
	assignments := make([]string, 0, len(spec.columns))
	for _, c := range spec.columns {
		if contains(spec.keyColumns, c) {
			continue
		}
		assignments = append(assignments, c+" = VALUES("+c+")")
	}
	base := "INSERT INTO " + spec.name + " (" + strings.Join(spec.columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if len(assignments) == 0 {
		return base
	}
	return base + " ON DUPLICATE KEY UPDATE " + strings.Join(assignments, ", ")
}

// writeTable 在单表事务里逐行 upsert（幂等）。
func writeTable(ctx context.Context, db *sql.DB, spec tableSpec, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	stmt := upsertSQL(spec)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启目标表 %s 事务失败: %w", spec.name, err)
	}
	for _, row := range rows {
		if _, err := tx.ExecContext(ctx, stmt, row...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("写入目标表 %s 失败: %w", spec.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目标表 %s 事务失败: %w", spec.name, err)
	}
	return nil
}

// verifyTable 比对目标表条数与抽样行；差异写进 rep.mismatches。
func verifyTable(ctx context.Context, dst *sql.DB, spec tableSpec, rows [][]any, sampleSize int, rep *mergeReport) error {
	dest, err := countRows(ctx, dst, spec.name)
	if err != nil {
		return err
	}
	rep.dest = dest
	if dest != rep.source {
		rep.mismatches = append(rep.mismatches, fmt.Sprintf("条数不一致: 源 %d / 目标 %d", rep.source, dest))
	}
	sampled := 0
	for _, row := range rows {
		if sampled >= sampleSize {
			break
		}
		sampled++
		destRow, err := readByKey(ctx, dst, spec, row)
		if err != nil {
			return err
		}
		key := spec.keyColumns[0]
		if destRow == nil {
			rep.mismatches = append(rep.mismatches, fmt.Sprintf("抽样缺行: %s=%v", key, row[keyIndex(spec.columns, key)]))
			continue
		}
		if !rowsEqual(row, destRow) {
			rep.mismatches = append(rep.mismatches, fmt.Sprintf("抽样不一致: %s=%v", key, row[keyIndex(spec.columns, key)]))
		}
	}
	rep.sampleRows = sampled
	return nil
}

// countRows 统计表行数。
func countRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计表 %s 失败: %w", table, err)
	}
	return n, nil
}

// readByKey 按键列从目标表读回一行（用源行的键值定位）。
func readByKey(ctx context.Context, db *sql.DB, spec tableSpec, srcRow []any) ([]any, error) {
	keyVals := make([]any, len(spec.keyColumns))
	where := make([]string, len(spec.keyColumns))
	for i, k := range spec.keyColumns {
		keyVals[i] = srcRow[keyIndex(spec.columns, k)]
		where[i] = k + " = ?"
	}
	query := "SELECT " + strings.Join(spec.columns, ", ") + " FROM " + spec.name + " WHERE " + strings.Join(where, " AND ")
	raw := make([]any, len(spec.columns))
	ptrs := make([]any, len(spec.columns))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	err := db.QueryRowContext(ctx, query, keyVals...).Scan(ptrs...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("抽样读取目标表 %s 失败: %w", spec.name, err)
	}
	return normalizeRow(spec, raw), nil
}

func keyIndex(cols []string, key string) int {
	for i, c := range cols {
		if c == key {
			return i
		}
	}
	return -1
}

// rowsEqual 比较两行列值（数字先归一为 float64，其余转字符串），消除驱动返回差异。
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

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// printTableReport 打印单表结果（不含任何连接串 / 口令）。
func printTableReport(rep mergeReport) {
	status := "OK"
	if len(rep.mismatches) > 0 {
		status = "不一致"
	}
	fmt.Printf("  %-16s 源 %5d  写入 %5d  目标 %5d  抽样 %d  %s\n",
		rep.table, rep.source, rep.written, rep.dest, rep.sampleRows, status)
	for _, m := range rep.mismatches {
		fmt.Println("      - " + m)
	}
}
