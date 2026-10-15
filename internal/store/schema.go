package store

import "context"

// ColumnSpec 描述一个可增量补齐的列：两库类型显式给出（不下沉成公共子集）。
type ColumnSpec struct {
	Name       string
	SQLiteType string
	MySQLType  string
}

// alterSpecs 是各表用于「增量补列」的列定义。只列需要防老库缺列的列；
// 建表语句见各方言 Migrate（CREATE TABLE IF NOT EXISTS）。
//
// 老库可能缺 history_samples.value_text（旧版本才有该列），补齐后新代码仍不写入。
var alterSpecs = map[string][]ColumnSpec{
	"history_samples": {
		{Name: "value_text", SQLiteType: "TEXT", MySQLType: "TEXT NULL"},
	},
}

// ensureOrder 是执行「增量补列」的表顺序（确定性，便于复现）。
var ensureOrder = []string{"history_samples"}

// MigrateTables 返回需要执行增量补列的表（顺序确定）。
func MigrateTables() []string { return ensureOrder }

// EnsureAlterColumns 逐表补齐缺失列（幂等）。name 为方言名。
func EnsureAlterColumns(ctx context.Context, d Dialect, q Querier, table string) error {
	for _, spec := range alterSpecs[table] {
		exists, err := d.ColumnExists(ctx, q, table, spec.Name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		typ := spec.MySQLType
		if d.Name() == "sqlite" {
			typ = spec.SQLiteType
		}
		if _, err := q.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+spec.Name+" "+typ); err != nil {
			return err
		}
	}
	return nil
}

// CopyTables 是跨库搬运的表顺序。
var CopyTables = []string{"history_samples", "users", "sessions"}

// copyColumns 是各表的显式列顺序（与建表列一致，供逐行拷贝）。
var copyColumns = map[string][]string{
	"history_samples": {"id", "platform_id", "time", "value", "value_text"},
	"users":           {"id", "username", "password_hash", "created_at"},
	"sessions":        {"token", "username", "created_at", "expires_at"},
}

// floatColumns 是跨库搬运时需要归一化为 float64 的列（消除驱动对数值列的返回差异）。
var floatColumns = map[string]bool{
	"history_samples.value": true,
}

// keyColumns 是 upsert 的冲突键。
var keyColumns = map[string][]string{
	"history_samples": {"id"},
	"users":           {"id"},
	"sessions":        {"token"},
}

// CopyColumns 返回某表的拷贝列顺序。
func CopyColumns(table string) []string { return copyColumns[table] }

// KeyColumns 返回某表的 upsert 键列。
func KeyColumns(table string) []string { return keyColumns[table] }

// IsFloatColumn 判断某列是否为跨库需归一化为 float64 的数值列。
func IsFloatColumn(table, column string) bool { return floatColumns[table+"."+column] }
