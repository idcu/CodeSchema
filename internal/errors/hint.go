package errors

// Hint 按错误码返回一条面向 Agent 的修复建议。
//
// 设计动机（对齐 FastContext 的错误 hint 思想）：Agent 拿到一个错误码时，
// 要么自己猜怎么修（多花一轮工具调用），要么原样报错给用户。把「这类错误的
// 常见修法」随错误一起回传，能让 Agent 一轮内自愈，直接减少工具调用轮次。
//
// 约束：
//   - 文案面向排障（说清该改哪个参数 / 该调哪个工具），不含堆栈、不含内部路径；
//   - 未知错误码返回空串，调用方据此决定要不要输出 hint 字段（不硬塞空信息）。
func Hint(code string) string {
	switch code {
	case "ERR_SYMBOL_NOT_FOUND":
		return "符号未命中：确认用的是全限定名（类 FQN，或 类FQN.方法名）；可先调 search_symbols 检索确认写法，多租户场景确认 project 参数指向了正确的仓库，或该仓库的索引是否已重建。"
	case "ERR_INVALID_PARAMETER":
		return "参数非法：检查必填参数是否缺失、类型是否正确（数值型参数不要加引号）；context/impact/tests 支持 symbols[] 批量传参（数组），单符号仍用 symbol/method。"
	case "ERR_RATE_LIMITED":
		return "已触发限流：降低请求频率，或调高 server.rate_limit 配置；批量查询请改用 symbols[] 一次传多个符号，把 N 次调用压成 1 次。"
	case "ERR_UNAUTHORIZED":
		return "认证失败：请求头需携带 Authorization: Bearer <token>，且令牌与服务端 server.auth_token 一致；未启用认证时不要带该头。"
	case "ERR_METHOD_NOT_ALLOWED":
		return "方法不允许：核对接口的 HTTP 方法（查询类为 GET/POST，以 API 文档为准）。"
	case "ERR_INTERNAL":
		return "服务内部错误：请查看服务端日志定位；若稳定复现，携带本错误码与请求参数反馈。"
	default:
		return ""
	}
}

// WithHint 在错误码对应的建议存在时，把它拼到错误文案之后。
//
// 用于 MCP 这类「错误只有一个 message 字段」的协议：hint 只能内联进 message，
// 但用固定分隔符 [hint] 起头，调用方仍可按需解析。
func WithHint(code, message string) string {
	h := Hint(code)
	if h == "" {
		return message
	}
	return message + "\n[hint] " + h
}
