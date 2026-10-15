package store

import (
	"context"
	"fmt"

	"quotahub-go/internal/model"
)

// MaxPointsPerPlatform 是单平台历史采样保留上限（与旧实现一致）。
const MaxPointsPerPlatform = 2000

// RecordSample 写入一条采样点并裁剪到保留上限；platformId 为空或值非有限数返回 false。
func (db *DB) RecordSample(ctx context.Context, platformID string, value float64, at string) (bool, error) {
	if platformID == "" {
		return false, nil
	}
	err := db.Tx(ctx, func(q Querier) error {
		if _, err := q.ExecContext(ctx,
			"INSERT INTO history_samples(platform_id, time, value) VALUES (?, ?, ?)",
			platformID, at, value); err != nil {
			return err
		}
		return trimSamples(ctx, q, platformID)
	})
	if err != nil {
		return false, fmt.Errorf("写入历史采样失败: %w", err)
	}
	return true, nil
}

// trimSamples 只保留某平台最新 MaxPointsPerPlatform 点（按 time, id 降序）。
func trimSamples(ctx context.Context, q Querier, platformID string) error {
	_, err := q.ExecContext(ctx,
		`DELETE FROM history_samples
		 WHERE platform_id = ?
		   AND id NOT IN (
		     SELECT id FROM history_samples WHERE platform_id = ?
		     ORDER BY time DESC, id DESC LIMIT ?
		   )`,
		platformID, platformID, MaxPointsPerPlatform)
	return err
}

// Samples 按时间升序返回某平台的采样点。
func (db *DB) Samples(ctx context.Context, platformID string) ([]model.SamplePoint, error) {
	rows, err := db.SQL.QueryContext(ctx,
		"SELECT time, value FROM history_samples WHERE platform_id = ? ORDER BY time ASC, id ASC",
		platformID)
	if err != nil {
		return nil, fmt.Errorf("读取历史采样失败: %w", err)
	}
	defer rows.Close()
	out := make([]model.SamplePoint, 0)
	for rows.Next() {
		var p model.SamplePoint
		if err := rows.Scan(&p.T, &p.V); err != nil {
			return nil, fmt.Errorf("扫描历史采样失败: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LatestSample 返回某平台最新的一个采样点（面板当前值来源）；无数据返回 nil。
func (db *DB) LatestSample(ctx context.Context, platformID string) (*model.SamplePoint, error) {
	var p model.SamplePoint
	err := db.SQL.QueryRowContext(ctx,
		"SELECT time, value FROM history_samples WHERE platform_id = ? ORDER BY time DESC, id DESC LIMIT 1",
		platformID).Scan(&p.T, &p.V)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取最新采样失败: %w", err)
	}
	return &p, nil
}

// RemoveSamples 删除某平台的全部历史采样。
func (db *DB) RemoveSamples(ctx context.Context, platformID string) error {
	if platformID == "" {
		return nil
	}
	_, err := db.SQL.ExecContext(ctx, "DELETE FROM history_samples WHERE platform_id = ?", platformID)
	return err
}
