// Package jsonstore 提供 JSON 配置文件的原子读写。
//
// 语义对齐旧实现 lib/storage.ts：写入走「临时文件 → rename」，读取失败回落默认值；
// 序列化使用两空格缩进（对齐 JSON.stringify(data, null, 2)），且不转义 `<` `>` `&`
// （对齐 JSON.stringify 不做 HTML 转义）。差异：文件权限收紧为 0600、目录 0700（安全约定）。
package jsonstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// Read 读取并解析 JSON；文件不存在或解析失败时返回 fallback。
func Read[T any](file string, fallback T) T {
	data, err := os.ReadFile(file)
	if err != nil {
		return fallback
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return fallback
	}
	return out
}

// ReadArray 读取 JSON 数组；非数组或失败时返回空切片。
func ReadArray[T any](file string) []T {
	list := Read[[]T](file, nil)
	if list == nil {
		return []T{}
	}
	return list
}

// WriteAtomic 原子写入 JSON：先写临时文件再 rename，内容为两空格缩进、不转义 HTML。
func WriteAtomic(file string, data any) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	payload, err := MarshalIndent(data)
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// MarshalIndent 按 JSON.stringify(data, null, 2) 的口径序列化：两空格缩进、无尾随换行、
// 不转义 `<` `>` `&`。
func MarshalIndent(data any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
