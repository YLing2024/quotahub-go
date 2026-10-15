package httpapi

// 本文件集中定义响应体形状。统一用具名结构体而非 map，是为了让 JSON 键顺序与旧实现一致
// （Go 的 map 会按键排序，结构体按字段顺序输出）。

// okResponse 是仅含 ok 的成功响应。
type okResponse struct {
	OK bool `json:"ok"`
}

// valueResponse 是抓取类成功响应 `{ok,value}`。
type valueResponse struct {
	OK    bool    `json:"ok"`
	Value float64 `json:"value"`
}

// testResponse 是单平台测试成功响应 `{ok,value,testedAt}`。
type testResponse struct {
	OK       bool    `json:"ok"`
	Value    float64 `json:"value"`
	TestedAt string  `json:"testedAt"`
}

// fetchResponse 是单平台抓取成功响应 `{ok,value,fetchedAt}`。
type fetchResponse struct {
	OK        bool    `json:"ok"`
	Value     float64 `json:"value"`
	FetchedAt string  `json:"fetchedAt"`
}

// failResponse 是抓取类失败响应 `{ok:false,error}`。
type failResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}
