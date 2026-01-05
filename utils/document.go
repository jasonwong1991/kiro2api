package utils

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"kiro2api/types"
)

// SupportedDocumentFormats 支持的文档格式
var SupportedDocumentFormats = map[string]string{
	"application/pdf":      "pdf",
	"text/plain":           "txt",
	"text/markdown":        "md",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
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
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return extractXlsxContent(data)
	case "text/markdown":
		return &DocumentContent{Text: string(data)}, nil
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

	// ZIP-based formats: PK (0x50 0x4B)
	if data[0] == 0x50 && data[1] == 0x4B {
		// 检查是否是 XLSX
		if isXlsxFile(data) {
			return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
		// 检查是否是 DOCX
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

// isXlsxFile 检查 ZIP 文件是否是 XLSX
func isXlsxFile(data []byte) bool {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return false
	}

	for _, file := range zipReader.File {
		if file.Name == "xl/workbook.xml" {
			return true
		}
	}
	return false
}

// extractPDFContent 从 PDF 中提取文本内容
func extractPDFContent(data []byte) (*DocumentContent, error) {
	// 优先使用 pdfcpu 提取文本
	text := extractTextWithPdfcpu(data)

	// 如果 pdfcpu 提取失败，回退到简化实现
	if text == "" {
		text = extractTextFromPDFStreams(data)
	}

	result := &DocumentContent{
		Text:   strings.TrimSpace(text),
		Images: []types.CodeWhispererImage{},
	}

	if result.Text == "" {
		result.Text = "[PDF document uploaded - text extraction limited, content may be image-based or encrypted]"
	}

	return result, nil
}

// extractTextWithPdfcpu 使用 pdfcpu 库提取 PDF 文本
func extractTextWithPdfcpu(data []byte) string {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "pdf_extract_")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmpDir)

	// 写入临时 PDF 文件
	tmpPDF := filepath.Join(tmpDir, "input.pdf")
	if err := os.WriteFile(tmpPDF, data, 0644); err != nil {
		return ""
	}

	// 使用 pdfcpu 提取内容流
	conf := model.NewDefaultConfiguration()
	if err := api.ExtractContentFile(tmpPDF, tmpDir, nil, conf); err != nil {
		return ""
	}

	// 读取提取的内容文件并解析文本
	var textParts []string
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		return ""
	}

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".txt") {
			content, err := os.ReadFile(filepath.Join(tmpDir, file.Name()))
			if err != nil {
				continue
			}
			// 从内容流中提取纯文本
			texts := extractTextFromPDFContentStream(string(content))
			textParts = append(textParts, texts...)
		}
	}

	// 过滤并清理结果
	var cleanedParts []string
	for _, part := range textParts {
		cleaned := cleanPDFText(part)
		if cleaned != "" {
			cleanedParts = append(cleanedParts, cleaned)
		}
	}

	return strings.Join(cleanedParts, " ")
}

// extractTextFromPDFContentStream 从 PDF 内容流中提取纯文本
func extractTextFromPDFContentStream(content string) []string {
	var textParts []string

	// 匹配 Tj 操作符: (text)Tj 或 (text) Tj
	tjPattern := regexp.MustCompile(`\(([^)]*)\)\s*Tj`)
	tjMatches := tjPattern.FindAllStringSubmatch(content, -1)
	for _, match := range tjMatches {
		if len(match) > 1 {
			text := decodePDFString(match[1])
			if text != "" {
				textParts = append(textParts, text)
			}
		}
	}

	// 匹配 TJ 数组: [(text) num (text)] TJ
	tjArrayPattern := regexp.MustCompile(`\[(.*?)\]\s*TJ`)
	tjArrayMatches := tjArrayPattern.FindAllStringSubmatch(content, -1)
	for _, match := range tjArrayMatches {
		if len(match) > 1 {
			innerPattern := regexp.MustCompile(`\(([^)]*)\)`)
			innerMatches := innerPattern.FindAllStringSubmatch(match[1], -1)
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

	return textParts
}

// extractTextFromPDFStreams 从 PDF 流中提取文本
func extractTextFromPDFStreams(data []byte) string {
	var textParts []string

	// 提取并解压所有流内容
	decompressedStreams := extractAndDecompressPDFStreams(data)

	// 从解压后的流中提取文本（只处理包含 BT...ET 文本块的流）
	for _, streamContent := range decompressedStreams {
		// 检查流是否包含文本块标记
		if !strings.Contains(streamContent, "BT") || !strings.Contains(streamContent, "ET") {
			continue
		}
		texts := extractTextFromPDFContent(streamContent)
		if len(texts) > 0 {
			textParts = append(textParts, texts...)
		}
	}

	// 如果从流中没有提取到文本，尝试从原始数据中提取
	if len(textParts) == 0 {
		texts := extractTextFromPDFContent(string(data))
		textParts = append(textParts, texts...)
	}

	// 过滤并清理结果
	var cleanedParts []string
	for _, part := range textParts {
		cleaned := cleanPDFText(part)
		if cleaned != "" {
			cleanedParts = append(cleanedParts, cleaned)
		}
	}

	return strings.Join(cleanedParts, " ")
}

// cleanPDFText 清理 PDF 提取的文本，移除乱码
func cleanPDFText(s string) string {
	var result strings.Builder
	validCount := 0
	totalCount := 0

	for _, r := range s {
		totalCount++
		// 只保留可打印 ASCII、中文、常见标点
		if (r >= 32 && r < 127) || r == '\n' || r == '\r' || r == '\t' {
			result.WriteRune(r)
			validCount++
		} else if r >= 0x4E00 && r <= 0x9FFF {
			// 中文字符
			result.WriteRune(r)
			validCount++
		} else if r >= 0x3000 && r <= 0x303F {
			// 中文标点
			result.WriteRune(r)
			validCount++
		}
	}

	// 如果有效字符比例太低，认为是乱码
	if totalCount > 0 && float64(validCount)/float64(totalCount) < 0.5 {
		return ""
	}

	text := strings.TrimSpace(result.String())

	// 过滤掉太短或明显是 PDF 指令的内容
	if len(text) < 2 {
		return ""
	}

	return text
}

// extractAndDecompressPDFStreams 提取并解压 PDF 中的所有流
func extractAndDecompressPDFStreams(data []byte) []string {
	var streams []string
	content := string(data)

	// 查找所有对象及其流
	// PDF 对象格式: X Y obj ... stream\r\n...endstream ... endobj
	objPattern := regexp.MustCompile(`(?s)(\d+\s+\d+\s+obj.*?)endobj`)
	objMatches := objPattern.FindAllStringSubmatch(content, -1)

	for _, objMatch := range objMatches {
		if len(objMatch) < 2 {
			continue
		}
		objContent := objMatch[1]

		// 检查是否包含流
		streamIdx := strings.Index(objContent, "stream")
		if streamIdx == -1 {
			continue
		}

		// 查找流结束位置
		endstreamIdx := strings.Index(objContent[streamIdx:], "endstream")
		if endstreamIdx == -1 {
			continue
		}

		// 提取流数据（跳过 "stream" 后的换行符）
		streamStart := streamIdx + 6 // len("stream")
		// 跳过可能的 \r\n 或 \n
		if streamStart < len(objContent) && objContent[streamStart] == '\r' {
			streamStart++
		}
		if streamStart < len(objContent) && objContent[streamStart] == '\n' {
			streamStart++
		}

		streamData := objContent[streamStart : streamIdx+endstreamIdx]

		// 检查是否使用 FlateDecode 压缩
		if strings.Contains(objContent[:streamIdx], "/FlateDecode") ||
			strings.Contains(objContent[:streamIdx], "/Filter/FlateDecode") ||
			strings.Contains(objContent[:streamIdx], "/Filter /FlateDecode") {
			// 尝试解压
			decompressed := decompressFlateDecode([]byte(streamData))
			if decompressed != "" {
				streams = append(streams, decompressed)
			}
		} else {
			// 未压缩的流直接使用
			streams = append(streams, streamData)
		}
	}

	return streams
}

// decompressFlateDecode 解压 FlateDecode 压缩的数据
func decompressFlateDecode(data []byte) string {
	// 尝试使用 raw deflate 解压
	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()

	var buf bytes.Buffer
	_, err := io.Copy(&buf, reader)
	if err != nil {
		// 如果 raw deflate 失败，尝试跳过 zlib 头（2字节）
		if len(data) > 2 {
			reader2 := flate.NewReader(bytes.NewReader(data[2:]))
			defer reader2.Close()
			buf.Reset()
			_, err = io.Copy(&buf, reader2)
			if err != nil {
				return ""
			}
		} else {
			return ""
		}
	}

	return buf.String()
}

// extractTextFromPDFContent 从 PDF 内容中提取文本
func extractTextFromPDFContent(content string) []string {
	var textParts []string

	// 查找 BT...ET 文本块中的 Tj/TJ 操作符
	btPattern := regexp.MustCompile(`(?s)BT(.*?)ET`)
	btMatches := btPattern.FindAllStringSubmatch(content, -1)

	for _, match := range btMatches {
		if len(match) > 1 {
			blockContent := match[1]

			// 提取 Tj 操作符中的文本: (text) Tj 或 <hex> Tj
			// 括号格式
			tjPattern := regexp.MustCompile(`\(([^)]*)\)\s*Tj`)
			tjMatches := tjPattern.FindAllStringSubmatch(blockContent, -1)
			for _, tj := range tjMatches {
				if len(tj) > 1 {
					text := decodePDFString(tj[1])
					if text != "" {
						textParts = append(textParts, text)
					}
				}
			}

			// 十六进制格式: <hex> Tj
			hexTjPattern := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*Tj`)
			hexTjMatches := hexTjPattern.FindAllStringSubmatch(blockContent, -1)
			for _, htj := range hexTjMatches {
				if len(htj) > 1 {
					text := decodeHexPDFString(htj[1])
					if text != "" {
						textParts = append(textParts, text)
					}
				}
			}

			// 提取 TJ 操作符中的文本: [(text) num (text)] TJ
			tjArrayPattern := regexp.MustCompile(`\[(.*?)\]\s*TJ`)
			tjArrayMatches := tjArrayPattern.FindAllStringSubmatch(blockContent, -1)
			for _, tja := range tjArrayMatches {
				if len(tja) > 1 {
					// 括号格式
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

					// 十六进制格式
					hexInnerPattern := regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
					hexInnerMatches := hexInnerPattern.FindAllStringSubmatch(tja[1], -1)
					for _, hexInner := range hexInnerMatches {
						if len(hexInner) > 1 {
							text := decodeHexPDFString(hexInner[1])
							if text != "" {
								textParts = append(textParts, text)
							}
						}
					}
				}
			}
		}
	}

	return textParts
}

// decodeHexPDFString 解码十六进制 PDF 字符串
func decodeHexPDFString(hex string) string {
	// 移除空格
	hex = strings.ReplaceAll(hex, " ", "")

	var result strings.Builder

	// 尝试作为 UTF-16BE 解码（每4个十六进制字符 = 1个字符）
	if len(hex)%4 == 0 {
		for i := 0; i+4 <= len(hex); i += 4 {
			var codePoint uint16
			_, err := fmt.Sscanf(hex[i:i+4], "%04X", &codePoint)
			if err != nil {
				continue
			}
			// 跳过控制字符和无效字符
			if codePoint >= 32 && codePoint != 0xFFFF {
				result.WriteRune(rune(codePoint))
			}
		}
	} else if len(hex)%2 == 0 {
		// 尝试作为单字节解码
		for i := 0; i+2 <= len(hex); i += 2 {
			var b byte
			_, err := fmt.Sscanf(hex[i:i+2], "%02X", &b)
			if err != nil {
				continue
			}
			if b >= 32 && b < 127 {
				result.WriteByte(b)
			}
		}
	}

	return result.String()
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

// extractXlsxContent 从 Excel (.xlsx) 中提取文本内容
func extractXlsxContent(data []byte) (*DocumentContent, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("XLSX 解析失败: %v", err)
	}

	// 读取共享字符串表
	sharedStrings := extractSharedStrings(zipReader)

	var textBuilder strings.Builder

	// 读取所有工作表
	for _, file := range zipReader.File {
		if strings.HasPrefix(file.Name, "xl/worksheets/sheet") && strings.HasSuffix(file.Name, ".xml") {
			rc, err := file.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}

			// 提取工作表文本
			sheetText := extractTextFromSheetXML(string(content), sharedStrings)
			if sheetText != "" {
				if textBuilder.Len() > 0 {
					textBuilder.WriteString("\n\n")
				}
				textBuilder.WriteString(sheetText)
			}
		}
	}

	result := &DocumentContent{
		Text:   strings.TrimSpace(textBuilder.String()),
		Images: []types.CodeWhispererImage{},
	}

	if result.Text == "" {
		result.Text = "[Excel document - no extractable text content]"
	}

	return result, nil
}

// extractSharedStrings 从 XLSX 中提取共享字符串表
func extractSharedStrings(zipReader *zip.Reader) []string {
	var sharedStrings []string

	for _, file := range zipReader.File {
		if file.Name == "xl/sharedStrings.xml" {
			rc, err := file.Open()
			if err != nil {
				return sharedStrings
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return sharedStrings
			}

			// 提取 <t>...</t> 标签中的文本
			re := regexp.MustCompile(`<t[^>]*>([^<]*)</t>`)
			matches := re.FindAllStringSubmatch(string(content), -1)
			for _, match := range matches {
				if len(match) > 1 {
					sharedStrings = append(sharedStrings, match[1])
				}
			}
			break
		}
	}

	return sharedStrings
}

// extractTextFromSheetXML 从工作表 XML 中提取文本
func extractTextFromSheetXML(xml string, sharedStrings []string) string {
	var rows []string

	// 匹配行 <row>...</row>
	rowRe := regexp.MustCompile(`<row[^>]*>(.*?)</row>`)
	rowMatches := rowRe.FindAllStringSubmatch(xml, -1)

	for _, rowMatch := range rowMatches {
		if len(rowMatch) < 2 {
			continue
		}

		var cells []string
		rowContent := rowMatch[1]

		// 匹配单元格 <c>...</c>
		cellRe := regexp.MustCompile(`<c[^>]*(?:t="([^"]*)")?[^>]*>(?:<v>([^<]*)</v>)?(?:<is><t>([^<]*)</t></is>)?</c>`)
		cellMatches := cellRe.FindAllStringSubmatch(rowContent, -1)

		for _, cellMatch := range cellMatches {
			cellType := cellMatch[1]  // t 属性
			cellValue := cellMatch[2] // <v> 值
			inlineStr := cellMatch[3] // <is><t> 内联字符串

			var text string
			if inlineStr != "" {
				text = inlineStr
			} else if cellType == "s" && cellValue != "" {
				// 共享字符串引用
				idx := 0
				fmt.Sscanf(cellValue, "%d", &idx)
				if idx >= 0 && idx < len(sharedStrings) {
					text = sharedStrings[idx]
				}
			} else if cellValue != "" {
				text = cellValue
			}

			if text != "" {
				cells = append(cells, text)
			}
		}

		if len(cells) > 0 {
			rows = append(rows, strings.Join(cells, "\t"))
		}
	}

	return strings.Join(rows, "\n")
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
