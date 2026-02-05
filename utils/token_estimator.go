package utils

import (
	"math"
	"strings"

	"kiro2api/config"
	"kiro2api/types"
)

// TokenEstimator 本地token估算器
// 设计原则：
// - KISS: 简单高效的估算算法，避免引入复杂的tokenizer库
// - 向后兼容: 支持所有Claude模型和消息格式
// - 性能优先: 本地计算，响应时间<5ms
type TokenEstimator struct{}

// NewTokenEstimator 创建token估算器实例
func NewTokenEstimator() *TokenEstimator {
	return &TokenEstimator{}
}

// EstimateTokens 估算消息的token数量
// 算法说明：
// - 基础估算: 英文约3.5-4字符/token，中文约1字符/token
// - 固定开销: 消息角色标记、JSON结构等
// - 工具开销: 每个工具定义约50-200 tokens
//
// 注意：此为快速估算，与官方tokenizer可能有±10%误差
func (e *TokenEstimator) EstimateTokens(req *types.CountTokensRequest) int {
	totalTokens := 0

	// 1. 系统提示词（system prompt）
	for _, sysMsg := range req.System {
		if sysMsg.Text != "" {
			totalTokens += e.EstimateTextTokens(sysMsg.Text)
			totalTokens += 2 // 系统提示的固定开销（role/结构字段等）
		}
	}

	// 2. 消息内容（messages）
	for _, msg := range req.Messages {
		// 角色标记开销（"user"/"assistant" + JSON结构）
		totalTokens += 3

		// 消息内容
		switch content := msg.Content.(type) {
		case string:
			// 文本消息
			totalTokens += e.EstimateTextTokens(content)
		case []any:
			// 复杂内容块（文本、图片、文档等）
			for _, block := range content {
				totalTokens += e.estimateContentBlock(block)
			}
		case []types.ContentBlock:
			// 类型化内容块
			for _, block := range content {
				totalTokens += e.estimateTypedContentBlock(block)
			}
		default:
			// 其他格式：JSON/结构化内容的token密度通常高于自然语言
			if jsonBytes, err := SafeMarshal(content); err == nil {
				totalTokens += e.EstimateTextTokens(string(jsonBytes))
			}
		}
	}

	// 3. 工具定义（tools）
	toolCount := len(req.Tools)
	if toolCount > 0 {
		// 工具开销策略：根据工具数量自适应调整
		// - 少量工具（1-3个）：每个工具高开销（包含大量元数据和结构信息）
		// - 大量工具（10+个）：共享开销 + 小增量（避免线性叠加过高）
		var baseToolsOverhead int
		var perToolOverhead int

		if toolCount == 1 {
			// 单工具场景：高开销（包含tools数组初始化、类型信息等）
			// 优化：平衡简单工具(403)和复杂工具(874)的估算
			baseToolsOverhead = 0
			perToolOverhead = 320 // 最优平衡值
		} else if toolCount <= 5 {
			// 少量工具：中等开销
			baseToolsOverhead = config.BaseToolsOverhead // 从150降至100
			perToolOverhead = 120                        // 从150降至120
		} else {
			// 大量工具：共享开销 + 低增量
			baseToolsOverhead = 180 // 从250降至180
			perToolOverhead = 60    // 从80降至60
		}

		totalTokens += baseToolsOverhead

		for _, tool := range req.Tools {
			// 工具名称（特殊处理：下划线分词导致token数增加）
			nameTokens := e.estimateToolName(tool.Name)
			totalTokens += nameTokens

			// 工具描述
			totalTokens += e.EstimateTextTokens(tool.Description)

			// 工具schema（JSON Schema）
			if tool.InputSchema != nil {
				if jsonBytes, err := SafeMarshal(tool.InputSchema); err == nil {
					// Schema编码密度：根据工具数量自适应
					// JSON Schema 结构化内容 token 密度较高
					var schemaCharsPerToken float64
					if toolCount == 1 {
						schemaCharsPerToken = 1.8 // 单工具：Schema通常偏结构化且字段重复，token更密集
					} else if toolCount <= 5 {
						schemaCharsPerToken = 1.9 // 少量工具
					} else {
						schemaCharsPerToken = 2.0 // 大量工具：共享结构存在，但JSON仍偏"费token"
					}

					schemaLen := len(jsonBytes)
					schemaTokens := int(math.Ceil(float64(schemaLen) / schemaCharsPerToken))  // 进一法

					// $schema字段URL开销（优化：降低开销）
					if strings.Contains(string(jsonBytes), "$schema") {
						if toolCount == 1 {
							schemaTokens += 10 // 从15降至10
						} else {
							schemaTokens += 5 // 从8降至5
						}
					}

					// 最小schema开销（优化：降低最小值）
					minSchemaTokens := 50 // 从80降至50
					if toolCount > 5 {
						minSchemaTokens = 30 // 从40降至30
					}
					if schemaTokens < minSchemaTokens {
						schemaTokens = minSchemaTokens
					}

					totalTokens += schemaTokens
				}
			}

			totalTokens += perToolOverhead
		}
	}

	// 4. 基础请求开销（API格式固定开销）
	// 优化：根据官方测试调整
	totalTokens += 3 // 调整至3以匹配官方

	return totalTokens
}

// estimateToolName 估算工具名称的token数量
// 工具名称通常包含下划线、驼峰等特殊结构，tokenizer会进行更细粒度的分词
// 例如: "mcp__Playwright__browser_navigate_back"
// 可能被分为: ["mcp", "__", "Play", "wright", "__", "browser", "_", "navigate", "_", "back"]
func (e *TokenEstimator) estimateToolName(name string) int {
	if name == "" {
		return 0
	}

	// 基础估算：按字符长度（使用进一法）
	baseTokens := (len(name) + 1) / 2 // 工具名称通常极其密集（比普通文本密集2倍）

	// 下划线分词惩罚：每个下划线可能导致额外的token
	underscoreCount := strings.Count(name, "_")
	underscorePenalty := underscoreCount // 每个下划线约1个额外token

	// 驼峰分词惩罚：大写字母可能是分词边界
	camelCaseCount := 0
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			camelCaseCount++
		}
	}
	camelCasePenalty := camelCaseCount / 2 // 每2个大写字母约1个额外token

	totalTokens := baseTokens + underscorePenalty + camelCasePenalty
	if totalTokens < 2 {
		totalTokens = 2 // 最少2个token
	}

	return totalTokens
}

// EstimateTextTokens 估算纯文本的token数量
// 混合语言处理：
// - 检测中文字符比例
// - 中文: 约 1 字符/token（纯中文短文本存在少量固定开销）
// - 英文: 自然语言约 3.5-4 字符/token（Claude），结构化内容更"费token"
func (e *TokenEstimator) EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}

	// 转换为rune数组以正确计算Unicode字符数
	runes := []rune(text)
	runeCount := len(runes)

	if runeCount == 0 {
		return 0
	}

	// 统计中文字符数和中文标点数（扫描全部字符）
	chineseChars := 0
	chinesePunctuation := 0
	for _, r := range runes {
		// 中文字符范围（CJK统一汉字）
		if r >= 0x4E00 && r <= 0x9FFF {
			chineseChars++
		}
		// 中文标点符号范围
		if (r >= 0x3000 && r <= 0x303F) || // CJK标点符号
			(r >= 0xFF00 && r <= 0xFFEF) { // 全角ASCII和标点
			chinesePunctuation++
		}
	}

	// 混合语言token估算
	// 根据官方测试数据精确校准：
	// 纯中文: '你'(1字符)→2tokens, '你好'(2字符)→3tokens
	// 混合: '你好hello'(2中+5英)→4tokens = 2中文 + 2英文
	// 结论: 纯中文有基础开销，混合文本无额外开销

	nonChineseChars := runeCount - chineseChars - chinesePunctuation

	// 判断是否为纯中文
	isPureChinese := (nonChineseChars == 0)

	// 中文token计算（包含中文标点）
	chineseTokens := 0
	if chineseChars > 0 {
		if isPureChinese {
			chineseTokens = 1 + chineseChars + chinesePunctuation // 纯中文: 基础1 + 字符数 + 标点数
		} else {
			chineseTokens = chineseChars + chinesePunctuation // 混合文本: 字符数 + 标点数
		}
	}

	// 非中文部分：区分自然语言 vs 结构化内容(JSON/代码/日志等)
	// 经验规律（Claude）：
	// - 自然语言平均约 3.5-4 字符/token，短文本更"费token"
	// - 结构化内容标点/分隔符密集，token密度显著更高（更容易低估）
	//
	// 重要：不再对长文本做全局"压缩系数"处理。
	// 长文本往往来自 tool_result / JSON / 代码块，其token密度不一定随长度降低，
	// 之前的压缩会在这些场景产生系统性低估（可达 40%+）。
	nonChineseTokens := 0
	if nonChineseChars > 0 {
		isStructured := looksLikeStructuredText(text)

		var charsPerToken float64

		if isStructured {
			// JSON/代码/日志：更"费token"，使用更保守的字符密度
			if nonChineseChars < 200 {
				charsPerToken = 2.0
			} else if nonChineseChars < 2000 {
				charsPerToken = 2.2
			} else {
				charsPerToken = 2.4
			}
		} else {
			// 自然语言：更接近 Claude 的常见密度
			if nonChineseChars < 50 {
				charsPerToken = 4.0
			} else if nonChineseChars < 200 {
				charsPerToken = 4.2
			} else if nonChineseChars < 2000 {
				charsPerToken = 4.5
			} else {
				charsPerToken = 4.6
			}
		}

		nonChineseTokens = int(math.Ceil(float64(nonChineseChars) / charsPerToken)) // 进一法
		if nonChineseTokens < 1 {
			nonChineseTokens = 1 // 至少1 token
		}
	}

	tokens := chineseTokens + nonChineseTokens

	if tokens < 1 {
		tokens = 1 // 最少1个token
	}

	return tokens
}

// looksLikeStructuredText 判断文本是否更像结构化内容（JSON/代码/日志）。
// 该判断用于选择更保守的字符密度，避免对 tool_result / Schema 等场景系统性低估。
func looksLikeStructuredText(text string) bool {
	if text == "" {
		return false
	}

	// 代码块通常 token 密度更高
	if strings.Contains(text, "```") {
		return true
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	// JSON 常见特征：以 {/[ 开头且包含冒号
	first := trimmed[0]
	if (first == '{' || first == '[') && strings.Contains(trimmed, ":") {
		return true
	}

	// 标点/分隔符密度：结构化文本通常包含大量括号、引号、冒号、逗号、反斜杠、下划线等
	total := 0
	structural := 0
	for _, r := range trimmed {
		// 中文汉字不参与结构密度判定（避免中文长文被误判为结构化）
		if r >= 0x4E00 && r <= 0x9FFF {
			total++
			continue
		}

		total++
		switch r {
		case '{', '}', '[', ']', ':', ',', '"', '\\', '=', '<', '>', '(', ')', ';', '_':
			structural++
		case '\n', '\r', '\t':
			structural++
		}
	}

	// 过短文本用密度判断容易误判，保守起见仅对足够长的文本生效
	if total < 80 {
		return false
	}

	return float64(structural)/float64(total) >= 0.12
}

// EstimateToolUseTokens 精确估算工具调用的token数量
// 用于非流式响应，基于实际的工具调用信息进行精确计算
//
// 参数:
//   - toolName: 工具名称
//   - toolInput: 工具参数（map[string]any）
//
// 返回:
//   - 估算的token数量
//
// Token组成:
//   - 结构字段: "type", "id", "name", "input" 关键字
//   - 工具名称: 使用estimateToolName精确计算
//   - 参数内容: JSON序列化后的token数
//
// 设计原则:
//   - 精确计算: 基于实际工具调用信息，而非简单系数
//   - 一致性: 与EstimateTokens中的工具定义计算保持一致的方法
//   - 适用场景: 非流式响应（handlers.go），有完整工具信息
func (e *TokenEstimator) EstimateToolUseTokens(toolName string, toolInput map[string]any) int {
	totalTokens := 0

	// 1. JSON结构字段开销
	// "type": "tool_use" ≈ 3 tokens
	totalTokens += 3

	// "id": "toolu_01A09q90qw90lq917835lq9" ≈ 8 tokens
	// (固定格式的UUID，约8个token)
	totalTokens += 8

	// "name" 关键字 ≈ 1 token
	totalTokens += 1

	// 2. 工具名称（使用与输入侧相同的精确方法）
	nameTokens := e.estimateToolName(toolName)
	totalTokens += nameTokens

	// 3. "input" 关键字 ≈ 1 token
	totalTokens += 1

	// 4. 参数内容（JSON序列化）
	if len(toolInput) > 0 {
		if jsonBytes, err := SafeMarshal(toolInput); err == nil {
			// 使用标准的4字符/token比率
			inputTokens := len(jsonBytes) / config.TokenEstimationRatio
			totalTokens += inputTokens
		}
	} else {
		// 空参数对象 {} ≈ 1 token
		totalTokens += 1
	}

	return totalTokens
}

// estimateContentBlock 估算单个内容块的token数量（通用map格式）
// 支持的内容类型：
// - text: 文本块
// - image: 图片（基于尺寸动态计算）
// - document: 文档（根据内容估算）
func (e *TokenEstimator) estimateContentBlock(block any) int {
	blockMap, ok := block.(map[string]any)
	if !ok {
		return 10 // 未知格式，保守估算
	}

	blockType, _ := blockMap["type"].(string)

	switch blockType {
	case "text":
		// 文本块
		if text, ok := blockMap["text"].(string); ok {
			return e.EstimateTextTokens(text)
		}
		return 10

	case "image":
		// 图片 token 计算公式（Claude 3）：tokens = 85 + 170 × tiles
		// tiles = ceil(width/512) × ceil(height/512)
		// 由于无法获取实际尺寸，使用基于 base64 数据大小的估算
		if source, ok := blockMap["source"].(map[string]any); ok {
			if data, ok := source["data"].(string); ok && len(data) > 0 {
				// base64 数据大小粗略估算图片尺寸
				// 典型压缩比：JPEG ~10:1, PNG ~3:1
				// 假设平均 5:1，每像素 3 字节
				rawBytes := len(data) * 3 / 4 // base64 解码后大小
				estimatedPixels := rawBytes * 5 / 3
				// 假设正方形图片
				side := int(math.Sqrt(float64(estimatedPixels)))
				tiles := ((side + 511) / 512) * ((side + 511) / 512)
				if tiles < 1 {
					tiles = 1
				}
				if tiles > 16 {
					tiles = 16 // 最大 4x4 tiles
				}
				return 85 + 170*tiles
			}
		}
		// 无法获取数据时使用默认值（中等尺寸图片）
		return 1105 // 85 + 170*6 (约 1500x1500 像素)

	case "document":
		// 文档：根据 base64 数据大小估算
		if source, ok := blockMap["source"].(map[string]any); ok {
			if data, ok := source["data"].(string); ok && len(data) > 0 {
				// 文档通常是文本密集型，估算提取后的文本 token
				rawBytes := len(data) * 3 / 4
				// PDF/Word 文档平均每字节约 0.3-0.5 个可提取字符
				estimatedChars := rawBytes * 4 / 10
				return e.EstimateTextTokens(strings.Repeat("x", estimatedChars))
			}
		}
		return 500 // 默认值

	case "tool_use":
		// 工具调用（在历史消息中的 assistant 消息可能包含）
		toolName, _ := blockMap["name"].(string)
		toolInput, _ := blockMap["input"].(map[string]any)
		return e.EstimateToolUseTokens(toolName, toolInput)

	case "tool_result":
		// 工具执行结果
		// 注意：tool_result 外层的 JSON 结构本身也会消耗 token
		overhead := 8 // type/content 等基础字段
		if toolUseID, ok := blockMap["tool_use_id"].(string); ok && toolUseID != "" {
			overhead += e.EstimateTextTokens(toolUseID)
		} else {
			overhead += 4 // 缺省的 tool_use_id 开销
		}
		if isErr, ok := blockMap["is_error"].(bool); ok && isErr {
			overhead += 1
		}

		content := blockMap["content"]
		switch c := content.(type) {
		case string:
			return overhead + e.EstimateTextTokens(c)
		case []any:
			total := overhead
			for _, item := range c {
				total += e.estimateContentBlock(item)
			}
			return total
		default:
			// content 可能是 map/list 等结构化数据
			if jsonBytes, err := SafeMarshal(content); err == nil {
				return overhead + e.EstimateTextTokens(string(jsonBytes))
			}
			return overhead + 50
		}

	default:
		// 未知类型：使用 EstimateTextTokens 处理 JSON
		if jsonBytes, err := SafeMarshal(block); err == nil {
			return e.EstimateTextTokens(string(jsonBytes))
		}
		return 10
	}
}

// estimateTypedContentBlock 估算类型化内容块的token数量
func (e *TokenEstimator) estimateTypedContentBlock(block types.ContentBlock) int {
	switch block.Type {
	case "text":
		if block.Text != nil {
			return e.EstimateTextTokens(*block.Text)
		}
		return 10

	case "image":
		// 图片 token 计算公式（Claude 3）：tokens = 85 + 170 × tiles
		// 对于类型化内容块，尝试从 Source 获取数据大小
		if block.Source != nil && block.Source.Data != "" {
			rawBytes := len(block.Source.Data) * 3 / 4
			estimatedPixels := rawBytes * 5 / 3
			side := int(math.Sqrt(float64(estimatedPixels)))
			tiles := ((side + 511) / 512) * ((side + 511) / 512)
			if tiles < 1 {
				tiles = 1
			}
			if tiles > 16 {
				tiles = 16
			}
			return 85 + 170*tiles
		}
		return 1105 // 默认值

	case "tool_use":
		// 工具调用（在历史消息中的 assistant 消息可能包含）
		toolName := ""
		if block.Name != nil {
			toolName = *block.Name
		}
		toolInput := make(map[string]any)
		if block.Input != nil {
			if input, ok := (*block.Input).(map[string]any); ok {
				toolInput = input
			}
		}
		return e.EstimateToolUseTokens(toolName, toolInput)

	case "tool_result":
		// 工具执行结果
		// 注意：tool_result 外层结构也会消耗 token
		overhead := 8
		if block.ToolUseId != nil && *block.ToolUseId != "" {
			overhead += e.EstimateTextTokens(*block.ToolUseId)
		} else {
			overhead += 4
		}
		if block.IsError != nil && *block.IsError {
			overhead += 1
		}

		switch content := block.Content.(type) {
		case string:
			return overhead + e.EstimateTextTokens(content)
		case []any:
			total := overhead
			for _, item := range content {
				total += e.estimateContentBlock(item)
			}
			return total
		case []types.ContentBlock:
			total := overhead
			for _, item := range content {
				total += e.estimateTypedContentBlock(item)
			}
			return total
		default:
			// content 可能是 map/list 等结构化数据
			if jsonBytes, err := SafeMarshal(content); err == nil {
				return overhead + e.EstimateTextTokens(string(jsonBytes))
			}
			return overhead + 50
		}

	default:
		// 未知类型
		return 10
	}
}

// EstimateOpenAITokens 估算 OpenAI 格式请求的 token 数量
// 将 OpenAI 格式转换为内部计算逻辑
func (e *TokenEstimator) EstimateOpenAITokens(req *types.OpenAIRequest) int {
	totalTokens := 0

	// 1. 消息内容
	for _, msg := range req.Messages {
		// 角色标记开销
		totalTokens += 3

		// 系统消息额外开销
		if msg.Role == "system" {
			totalTokens += 2
		}

		// 消息内容
		switch content := msg.Content.(type) {
		case string:
			totalTokens += e.EstimateTextTokens(content)
		case []any:
			// OpenAI 多模态内容块
			for _, block := range content {
				totalTokens += e.estimateContentBlock(block)
			}
		default:
			if jsonBytes, err := SafeMarshal(content); err == nil {
				totalTokens += e.EstimateTextTokens(string(jsonBytes))
			}
		}

		// 工具调用 (assistant 消息中的 tool_calls)
		for _, toolCall := range msg.ToolCalls {
			// 工具调用结构开销
			totalTokens += 10 // type, id, function 关键字

			// 工具名称
			totalTokens += e.estimateToolName(toolCall.Function.Name)

			// 工具参数 (JSON 字符串)
			if toolCall.Function.Arguments != "" {
				totalTokens += e.EstimateTextTokens(toolCall.Function.Arguments)
			}
		}
	}

	// 2. 工具定义
	toolCount := len(req.Tools)
	if toolCount > 0 {
		var baseToolsOverhead int
		var perToolOverhead int

		if toolCount == 1 {
			baseToolsOverhead = 0
			perToolOverhead = 320
		} else if toolCount <= 5 {
			baseToolsOverhead = 100
			perToolOverhead = 120
		} else {
			baseToolsOverhead = 180
			perToolOverhead = 60
		}

		totalTokens += baseToolsOverhead

		for _, tool := range req.Tools {
			// 工具名称
			nameTokens := e.estimateToolName(tool.Function.Name)
			totalTokens += nameTokens

			// 工具描述
			totalTokens += e.EstimateTextTokens(tool.Function.Description)

			// 工具参数 schema
			if tool.Function.Parameters != nil {
				if jsonBytes, err := SafeMarshal(tool.Function.Parameters); err == nil {
					var schemaCharsPerToken float64
					if toolCount == 1 {
						schemaCharsPerToken = 1.8
					} else if toolCount <= 5 {
						schemaCharsPerToken = 1.9
					} else {
						schemaCharsPerToken = 2.0
					}

					schemaLen := len(jsonBytes)
					schemaTokens := int(math.Ceil(float64(schemaLen) / schemaCharsPerToken))

					if strings.Contains(string(jsonBytes), "$schema") {
						if toolCount == 1 {
							schemaTokens += 10
						} else {
							schemaTokens += 5
						}
					}

					minSchemaTokens := 50
					if toolCount > 5 {
						minSchemaTokens = 30
					}
					if schemaTokens < minSchemaTokens {
						schemaTokens = minSchemaTokens
					}

					totalTokens += schemaTokens
				}
			}

			totalTokens += perToolOverhead
		}
	}

	// 3. 基础请求开销
	totalTokens += 3

	return totalTokens
}

// IsValidClaudeModel 验证是否为有效的Claude模型
// 支持所有Claude系列模型（不限制具体版本号）
func IsValidClaudeModel(model string) bool {
	if model == "" {
		return false
	}

	model = strings.ToLower(model)

	// 支持的模型前缀
	validPrefixes := []string{
		"claude-",          // 所有Claude模型
		"gpt-",             // OpenAI兼容模式（codex渠道）
		"gemini-",          // Gemini兼容模式
		"text-",            // 传统completion模型
		"anthropic.claude", // Bedrock格式
	}

	for _, prefix := range validPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}

	return false
}
