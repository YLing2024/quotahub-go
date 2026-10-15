package sqlite

// DSN 组装 modernc.org/sqlite 的连接串。
//
// 与旧实现 connection.ts 的 pragma 口径一致（WAL、NORMAL），并补一个 busy_timeout 以避免
// 多连接并发下偶发 "database is locked"。文件路径按原样拼进 `file:` URI：调用方负责给出
// 合法路径（相对路径相对进程工作目录）。
func DSN(path string) string {
	return "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)"
}
