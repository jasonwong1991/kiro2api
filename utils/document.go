package utils

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"

	"kiro2api/types"
)

// SupportedDocumentFormats 支持的文档格式
var SupportedDocumentFormats = map[string]string{
	"application/pdf": "pdf",
	"text/plain":      "txt",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
}

// MaxDocumentSize 最大文档大小 (32MB)
const MaxDocumentSize = 32 * 1024 * 1024

// DocumentContent 文档处理结果
type DocumentContent struct {
	Text   string                     // 提取的文本内容
	Images []types.CodeWhispererImage // 提取的图片
}

// ProcessDocument 处理文档内容块，提取文本和图片
func ProcessDocument(doc *types.DocumentSource) (*DocumentContent, error) {
	if doc == nil {
		return nil, fmt.Errorf("文档数据为空")
	}

	switch doc.Type {
	case "base64":
		return processBase64Document(doc)
	case "text":
		// 纯文本文档直接返回
		return &DocumentContent{Text: doc.Data}, nil
	case "url":
		// URL 类型暂不支持（需要额外的 HTTP 请求）
		return nil, fmt.Errorf("暂不支持 URL 类型文档，请使用 base64 编码")
	default:
		return nil, fmt.Errorf("不支持的文档类型: %s", doc.Type)
	}
}

// processBase64Document 处理 base64 编码的文档
func processBase64Document(doc *types.DocumentSource) (*DocumentContent, error) {
	// 解码 base64 数据
	data, err := base64.StdEncoding.DecodeString(doc.Data)
	if err != nil {
		return nil, fmt.Errorf("base64 解码失败: %v", err)
	}

	// 检查文件大小
	if len(data) > MaxDocumentSize {
		return nil, fmt.Errorf("文档过大: %d 字节，最大支持 %d 字节", len(data), MaxDocumentSize)
	}

	// 根据 media_type 或自动检测处理
	mediaType := doc.MediaType
	if mediaType == "" {
		mediaType = detectDocumentFormat(data)
	}

	switch mediaType {
	case "application/pdf":
		return extractPDFContent(data)
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return extractDocxContent(data)
	case "text/plain":
		return &DocumentContent{Text: string(data)}, nil
	default:
		// 默认作为纯文本处理
		return &DocumentContent{Text: string(data)}, nil
	}
}

// detectDocumentFormat 检测文档格式
func detectDocumentFormat(data []byte) string {
	if len(data) < 8 {
		return "text/plain"
	}

	// PDF: %PDF-
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return "application/pdf"
	}

	// Word (.docx) / ZIP: PK
	if data[0] == 0x50 && data[1] == 0x4B {
		// 进一步检查是否是 DOCX
		if isDocxFile(data) {
			return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		}
	}

	return "text/plain"
}

// isDocxFile 检查 ZIP 文件是否是 DOCX
func isDocxFile(data []byte) bool {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return false
	}

	for _, file := range zipReader.File {
		if file.Name == "word/document.xml" {
			return true
		}
	}
	return false
}

// extractPDFContent 从 PDF 中提取文本内容 (简化实现)
func extractPDFContent(data []byte) (*DocumentContent, error) {
	// 简化的 PDF 文本提取：查找文本流
	text := extractTextFromPDFStreams(data)

	result := &DocumentContent{
		Text:   strings.TrimSpace(text),
		Images: []types.CodeWhispererImage{},
	}

	if result.Text == "" {
		result.Text = "[PDF document uploaded - text extraction limited, content may be image-based or encrypted]"
	}

	return result, nil
}

// extractTextFromPDFStreams 从 PDF 流中提取文本 (简化实现)
func extractTextFromPDFStreams(data []byte) string {
	content := string(data)
	var textParts []string

	// 方法1: 查找 BT...ET 文本块中的 Tj/TJ 操作符
	btPattern := regexp.MustCompile(`BT\s*(.*?)\s*ET`)
	btMatches := btPattern.FindAllStringSubmatch(content, -1)

	for _, match := range btMatches {
		if len(match) > 1 {
			// 提取 Tj 操作符中的文本: (text) Tj
			tjPattern := regexp.MustCompile(`\(([^)]*)\)\s*Tj`)
			tjMatches := tjPattern.FindAllStringSubmatch(match[1], -1)
			for _, tj := range tjMatches {
				if len(tj) > 1 {
					text := decodePDFString(tj[1])
					if text != "" {
						textParts = append(textParts, text)
					}
				}
			}

			// 提取 TJ 操作符中的文本: [(text) num (text)] TJ
			tjArrayPattern := regexp.MustCompile(`\[(.*?)\]\s*TJ`)
			tjArrayMatches := tjArrayPattern.FindAllStringSubmatch(match[1], -1)
			for _, tja := range tjArrayMatches {
				if len(tja) > 1 {
					innerPattern := regexp.MustCompile(`\(([^)]*)\)`)
					innerMatches := innerPattern.FindAllStringSubmatch(tja[1], -1)
					for _, inner := range innerMatches {
						if len(inner) > 1 {
							text := decodePDFString(inner[1])
							if text != "" {
								textParts = append(textParts, text)
							}
						}
					}
				}
			}
		}
	}

	// 方法2: 如果上述方法没有提取到文本，尝试查找可读的 ASCII 文本块
	if len(textParts) == 0 {
		// 查找连续的可打印 ASCII 字符
		asciiPattern := regexp.MustCompile(`[\x20-\x7E]{20,}`)
		asciiMatches := asciiPattern.FindAllString(content, 100)
		for _, match := range asciiMatches {
			// 过滤掉明显的 PDF 语法
			if !strings.Contains(match, "obj") &&
				!strings.Contains(match, "endobj") &&
				!strings.Contains(match, "stream") &&
				!strings.Contains(match, "/Type") &&
				!strings.Contains(match, "/Filter") {
				textParts = append(textParts, match)
			}
		}
	}

	return strings.Join(textParts, " ")
}

// decodePDFString 解码 PDF 字符串
func decodePDFString(s string) string {
	// 处理转义字符
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\r", "\r")
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\(", "(")
	s = strings.ReplaceAll(s, "\\)", ")")
	s = strings.ReplaceAll(s, "\\\\", "\\")

	// 过滤不可打印字符
	var result strings.Builder
	for _, r := range s {
		if r >= 32 && r < 127 || r == '\n' || r == '\r' || r == '\t' {
			result.WriteRune(r)
		} else if r >= 0x4E00 && r <= 0x9FFF {
			// 保留中文字符
			result.WriteRune(r)
		}
	}

	return result.String()
}

// extractDocxContent 从 Word (.docx) 中提取文本内容
func extractDocxContent(data []byte) (*DocumentContent, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("DOCX 解析失败: %v", err)
	}

	var textBuilder strings.Builder
	var images []types.CodeWhispererImage

	// 查找 word/document.xml
	for _, file := range zipReader.File {
		if file.Name == "word/document.xml" {
			rc, err := file.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}

			// 提取文本
			text := extractTextFromDocxXML(string(content))
			textBuilder.WriteString(text)
		}

		// 提取嵌入图片
		if strings.HasPrefix(file.Name, "word/media/") {
			img, err := extractImageFromZip(file)
			if err == nil && img != nil {
				images = append(images, *img)
			}
		}
	}

	result := &DocumentContent{
		Text:   strings.TrimSpace(textBuilder.String()),
		Images: images,
	}

	if result.Text == "" {
		result.Text = "[Word document - no extractable text content]"
	}

	return result, nil
}

// extractTextFromDocxXML 从 DOCX XML 中提取纯文本
func extractTextFromDocxXML(xml string) string {
	// 匹配 <w:t>...</w:t> 标签中的文本
	re := regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)
	matches := re.FindAllStringSubmatch(xml, -1)

	var parts []string
	for _, match := range matches {
		if len(match) > 1 && match[1] != "" {
			parts = append(parts, match[1])
		}
	}

	// 处理段落分隔
	result := strings.Join(parts, "")

	// 在段落结束处添加换行
	result = regexp.MustCompile(`</w:p>`).ReplaceAllString(xml, "\n")
	// 重新提取文本
	re2 := regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)
	matches2 := re2.FindAllStringSubmatch(result, -1)

	var finalParts []string
	for _, match := range matches2 {
		if len(match) > 1 {
			finalParts = append(finalParts, match[1])
		}
	}

	return strings.Join(finalParts, "")
}

// extractImageFromZip 从 ZIP 文件中提取图片
func extractImageFromZip(file *zip.File) (*types.CodeWhispererImage, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	// 检测图片格式
	mediaType, err := DetectImageFormat(data)
	if err != nil {
		return nil, err
	}

	format := GetImageFormatFromMediaType(mediaType)
	if format == "" {
		return nil, fmt.Errorf("不支持的图片格式: %s", mediaType)
	}

	return &types.CodeWhispererImage{
		Format: format,
		Source: struct {
			Bytes string `json:"bytes"`
		}{
			Bytes: base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}

// ValidateDocumentContent 验证文档内容
func ValidateDocumentContent(doc *types.DocumentSource) error {
	if doc == nil {
		return fmt.Errorf("文档数据为空")
	}

	if doc.Type == "" {
		return fmt.Errorf("文档类型为空")
	}

	switch doc.Type {
	case "base64":
		if doc.Data == "" {
			return fmt.Errorf("base64 文档数据为空")
		}
		// 验证 base64 编码
		data, err := base64.StdEncoding.DecodeString(doc.Data)
		if err != nil {
			return fmt.Errorf("无效的 base64 编码: %v", err)
		}
		if len(data) > MaxDocumentSize {
			return fmt.Errorf("文档过大: %d 字节，最大支持 %d 字节", len(data), MaxDocumentSize)
		}
	case "text":
		if doc.Data == "" {
			return fmt.Errorf("文本文档内容为空")
		}
	case "url":
		if doc.URL == "" {
			return fmt.Errorf("文档 URL 为空")
		}
	default:
		return fmt.Errorf("不支持的文档类型: %s", doc.Type)
	}

	return nil
}

// IsSupportedDocumentFormat 检查是否为支持的文档格式
func IsSupportedDocumentFormat(mediaType string) bool {
	_, ok := SupportedDocumentFormats[mediaType]
	return ok
}
