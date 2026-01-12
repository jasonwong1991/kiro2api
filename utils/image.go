package utils

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"strconv"
	"regexp"
	"strings"

	"kiro2api/logger"
	"kiro2api/types"

	"golang.org/x/image/draw"
)

// SupportedImageFormats 支持的图片格式
var SupportedImageFormats = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

// MaxImageSize 最大图片大小 (20MB)
const MaxImageSize = 20 * 1024 * 1024

// DefaultCodeWhispererMaxImageBytes CodeWhisperer 单张图片建议上限（原始字节数）。
// 可通过环境变量 CODEWHISPERER_MAX_IMAGE_BYTES 覆盖；设置为 0 可禁用压缩。
const DefaultCodeWhispererMaxImageBytes = 32 * 1024

func getCodeWhispererMaxImageBytes() int {
	v := strings.TrimSpace(os.Getenv("CODEWHISPERER_MAX_IMAGE_BYTES"))
	if v == "" {
		return DefaultCodeWhispererMaxImageBytes
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return DefaultCodeWhispererMaxImageBytes
	}
	if n < 0 {
		return 0
	}
	return n
}

// DetectImageFormat 检测图片格式
func DetectImageFormat(data []byte) (string, error) {
	if len(data) < 12 {
		return "", fmt.Errorf("文件太小，无法检测格式")
	}

	// 检测 JPEG
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return "image/jpeg", nil
	}

	// 检测 PNG
	if len(data) >= 8 &&
		data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 &&
		data[4] == 0x0D && data[5] == 0x0A && data[6] == 0x1A && data[7] == 0x0A {
		return "image/png", nil
	}

	// 检测 GIF
	if len(data) >= 6 &&
		((data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x38 && data[4] == 0x37 && data[5] == 0x61) ||
			(data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x38 && data[4] == 0x39 && data[5] == 0x61)) {
		return "image/gif", nil
	}

	// 检测 WebP
	if len(data) >= 12 &&
		data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return "image/webp", nil
	}

	// 检测 BMP
	if len(data) >= 2 && data[0] == 0x42 && data[1] == 0x4D {
		return "image/bmp", nil
	}

	return "", fmt.Errorf("不支持的图片格式")
}

// ProcessImageData 处理图片数据，检测格式并编码为 base64
// IsSupportedImageFormat 检查是否为支持的图片格式
func IsSupportedImageFormat(mediaType string) bool {
	// 以 GetImageFormatFromMediaType 为单一事实来源，避免多处维护
	return GetImageFormatFromMediaType(mediaType) != ""
}

// GetImageFormatFromMediaType 从 media type 获取图片格式
func GetImageFormatFromMediaType(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	default:
		return ""
	}
}

// CreateCodeWhispererImage 创建 CodeWhisperer 格式的图片对象
func CreateCodeWhispererImage(imageSource *types.ImageSource) *types.CodeWhispererImage {
	if imageSource == nil {
		return nil
	}

	format := GetImageFormatFromMediaType(imageSource.MediaType)
	if format == "" {
		return nil
	}

	return &types.CodeWhispererImage{
		Format: format,
		Source: struct {
			Bytes string `json:"bytes"`
		}{
			Bytes: imageSource.Data,
		},
	}
}

func resizeToMaxDimension(src image.Image, maxDim int) image.Image {
	if maxDim <= 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	if w <= maxDim && h <= maxDim {
		return src
	}

	var nw, nh int
	if w >= h {
		nw = maxDim
		nh = int(float64(h) * (float64(maxDim) / float64(w)))
	} else {
		nh = maxDim
		nw = int(float64(w) * (float64(maxDim) / float64(h)))
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// CompressImageForCodeWhisperer 将图片压缩到指定上限（优先转换为 JPEG + 缩放）。
// 失败时返回原始数据（KISS：不阻断请求，交由上游兜底）。
func CompressImageForCodeWhisperer(mediaType string, data []byte, maxBytes int) (outMediaType string, out []byte, _ error) {
	if maxBytes <= 0 || len(data) <= maxBytes {
		return mediaType, data, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		logger.Warn("图片解码失败，跳过压缩",
			logger.String("media_type", mediaType),
			logger.Int("size", len(data)),
			logger.Err(err))
		return mediaType, data, nil
	}

	// 逐步缩放 + 降低质量，直到满足 maxBytes
	maxDims := []int{1024, 768, 512, 384, 256, 192, 160, 128}
	qualities := []int{85, 75, 65, 55, 45, 35, 25}

	var best []byte
	for _, maxDim := range maxDims {
		resized := resizeToMaxDimension(img, maxDim)
		for _, q := range qualities {
			var buf bytes.Buffer
			encErr := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: q})
			if encErr != nil {
				continue
			}
			b := buf.Bytes()
			if best == nil || len(b) < len(best) {
				best = b
			}
			if len(b) <= maxBytes {
				logger.Debug("图片已压缩以满足CodeWhisperer限制",
					logger.String("from_media_type", mediaType),
					logger.String("to_media_type", "image/jpeg"),
					logger.Int("original_size", len(data)),
					logger.Int("compressed_size", len(b)),
					logger.Int("max_bytes", maxBytes),
					logger.Int("max_dim", maxDim),
					logger.Int("jpeg_quality", q))
				return "image/jpeg", b, nil
			}
		}
	}

	// 无法满足上限：返回最小版本（如果确实变小），否则返回原始
	if best != nil && len(best) < len(data) {
		logger.Warn("图片未能压缩到目标上限，使用最小版本",
			logger.String("from_media_type", mediaType),
			logger.String("to_media_type", "image/jpeg"),
			logger.Int("original_size", len(data)),
			logger.Int("compressed_size", len(best)),
			logger.Int("max_bytes", maxBytes))
		return "image/jpeg", best, nil
	}

	return mediaType, data, nil
}

// NormalizeImageData 通用图片规范化：解码后重新编码，清除非标准数据
// 使用 Go 标准库解码验证图片有效性，然后重新编码为干净的格式
// 这可以自动处理：JPEG 尾随字节、损坏的元数据、非标准编码等问题
func NormalizeImageData(mediaType string, data []byte) ([]byte, error) {
	if len(data) > MaxImageSize {
		return nil, fmt.Errorf("图片数据过大: %d 字节，最大支持 %d 字节", len(data), MaxImageSize)
	}

	// 检测实际格式
	detectedType, err := DetectImageFormat(data)
	if err != nil {
		return nil, fmt.Errorf("无法识别图片格式: %v", err)
	}
	if detectedType != mediaType {
		return nil, fmt.Errorf("图片格式不匹配: 声明为 %s，实际为 %s", mediaType, detectedType)
	}

	// 使用 Go 标准库解码图片（验证有效性）
	reader := bytes.NewReader(data)
	img, format, err := image.Decode(reader)
	if err != nil {
		// 解码失败，记录警告但返回原始数据（兼容不支持的子格式）
		logger.Warn("图片解码失败，使用原始数据",
			logger.String("media_type", mediaType),
			logger.Err(err))
		return data, nil
	}

	// 重新编码为干净的格式
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	case "png":
		err = png.Encode(&buf, img)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default:
		// 不支持重新编码的格式（webp/bmp），返回原始数据
		logger.Debug("图片格式不支持重新编码，使用原始数据",
			logger.String("format", format))
		return data, nil
	}

	if err != nil {
		logger.Warn("图片重新编码失败，使用原始数据",
			logger.String("format", format),
			logger.Err(err))
		return data, nil
	}

	normalized := buf.Bytes()

	// 关键修复：如果规范化后图片变大，使用原始数据
	// CodeWhisperer API 对图片大小有限制，避免不必要的膨胀
	if len(normalized) > len(data) {
		logger.Debug("规范化后图片变大，使用原始数据",
			logger.String("format", format),
			logger.Int("original_size", len(data)),
			logger.Int("normalized_size", len(normalized)))
		return data, nil
	}

	if len(normalized) != len(data) {
		logger.Debug("图片已规范化",
			logger.String("format", format),
			logger.Int("original_size", len(data)),
			logger.Int("normalized_size", len(normalized)))
	}

	return normalized, nil
}

// ParseImageFromContentBlock 从 ContentBlock 解析图片信息

// ValidateImageContent 验证图片内容的完整性并规范化
// 注意：此函数会修改 imageSource.Data 为规范化后的数据
func ValidateImageContent(imageSource *types.ImageSource) error {
	if imageSource == nil {
		return fmt.Errorf("图片数据为空")
	}

	if imageSource.Type != "base64" {
		return fmt.Errorf("不支持的图片类型: %s", imageSource.Type)
	}

	if !IsSupportedImageFormat(imageSource.MediaType) {
		return fmt.Errorf("不支持的图片格式: %s", imageSource.MediaType)
	}

	if imageSource.Data == "" {
		return fmt.Errorf("图片数据为空")
	}

	// 验证 base64 编码
	decodedData, err := base64.StdEncoding.DecodeString(imageSource.Data)
	if err != nil {
		return fmt.Errorf("无效的 base64 编码: %v", err)
	}

	if len(decodedData) > MaxImageSize {
		return fmt.Errorf("图片数据过大: %d 字节，最大支持 %d 字节", len(decodedData), MaxImageSize)
	}

	// 规范化图片数据（解码后重新编码，清除非标准数据）
	normalized, err := NormalizeImageData(imageSource.MediaType, decodedData)
	if err != nil {
		return err
	}

	// 如果超过 CodeWhisperer 的图片上限，尝试进一步压缩/缩放（对齐 Kiro App 行为）
	maxBytes := getCodeWhispererMaxImageBytes()
	outMediaType, outBytes, _ := CompressImageForCodeWhisperer(imageSource.MediaType, normalized, maxBytes)
	imageSource.MediaType = outMediaType
	imageSource.Data = base64.StdEncoding.EncodeToString(outBytes)

	return nil
}

// ParseDataURL 解析data URL，提取媒体类型和base64数据
// 返回的 base64Data 已经过规范化处理
func ParseDataURL(dataURL string) (mediaType, base64Data string, err error) {
	// data URL格式：data:[<mediatype>][;base64],<data>
	dataURLPattern := regexp.MustCompile(`^data:([^;,]+)(;base64)?,(.+)$`)

	matches := dataURLPattern.FindStringSubmatch(dataURL)
	if len(matches) != 4 {
		return "", "", fmt.Errorf("无效的data URL格式")
	}

	mediaType = matches[1]
	isBase64 := matches[2] == ";base64"
	data := matches[3]

	if !isBase64 {
		return "", "", fmt.Errorf("仅支持base64编码的data URL")
	}

	// 验证是否为支持的图片格式
	if !IsSupportedImageFormat(mediaType) {
		return "", "", fmt.Errorf("不支持的图片格式: %s", mediaType)
	}

	// 验证base64编码
	decodedData, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", "", fmt.Errorf("无效的base64编码: %v", err)
	}

	// 规范化图片数据
	normalized, err := NormalizeImageData(mediaType, decodedData)
	if err != nil {
		return "", "", err
	}

	// 对超大图片执行压缩/缩放（与 ValidateImageContent 一致）
	maxBytes := getCodeWhispererMaxImageBytes()
	outMediaType, outBytes, _ := CompressImageForCodeWhisperer(mediaType, normalized, maxBytes)

	return outMediaType, base64.StdEncoding.EncodeToString(outBytes), nil
}

// ConvertImageURLToImageSource 将OpenAI的image_url格式转换为Anthropic的ImageSource格式
func ConvertImageURLToImageSource(imageURL map[string]any) (*types.ImageSource, error) {
	// 获取URL字段
	urlValue, exists := imageURL["url"]
	if !exists {
		return nil, fmt.Errorf("image_url缺少url字段")
	}

	urlStr, ok := urlValue.(string)
	if !ok {
		return nil, fmt.Errorf("image_url的url字段必须是字符串")
	}

	// 检查是否是data URL
	if !strings.HasPrefix(urlStr, "data:") {
		return nil, fmt.Errorf("目前仅支持data URL格式的图片")
	}

	// 解析data URL
	mediaType, base64Data, err := ParseDataURL(urlStr)
	if err != nil {
		return nil, fmt.Errorf("解析data URL失败: %v", err)
	}

	return &types.ImageSource{
		Type:      "base64",
		MediaType: mediaType,
		Data:      base64Data,
	}, nil
}
