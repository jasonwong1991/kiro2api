package types

// MCP 请求/响应类型定义 (用于 WebSearch 工具调用)

// McpRequest MCP JSON-RPC 请求
type McpRequest struct {
	ID      string    `json:"id"`
	JsonRpc string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  McpParams `json:"params"`
}

// McpParams MCP 请求参数
type McpParams struct {
	Name      string       `json:"name"`
	Arguments McpArguments `json:"arguments"`
}

// McpArguments MCP 参数内容
type McpArguments struct {
	Query string `json:"query"`
}

// McpResponse MCP JSON-RPC 响应
type McpResponse struct {
	Error   *McpError  `json:"error,omitempty"`
	ID      string     `json:"id"`
	JsonRpc string     `json:"jsonrpc"`
	Result  *McpResult `json:"result,omitempty"`
}

// McpError MCP 错误
type McpError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// McpResult MCP 结果
type McpResult struct {
	Content []McpContent `json:"content"`
	IsError bool         `json:"isError"`
}

// McpContent MCP 内容块
type McpContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// WebSearchResults 搜索结果集合
type WebSearchResults struct {
	Results      []WebSearchResult `json:"results"`
	TotalResults int               `json:"totalResults,omitempty"`
	Query        string            `json:"query,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// WebSearchResult 单个搜索结果
type WebSearchResult struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Snippet       string `json:"snippet,omitempty"`
	PublishedDate int64  `json:"publishedDate,omitempty"`
	ID            string `json:"id,omitempty"`
	Domain        string `json:"domain,omitempty"`
}
