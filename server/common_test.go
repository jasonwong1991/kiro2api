package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"kiro2api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// MockAuthService 用于测试的mock AuthService
type MockAuthService struct {
	token      types.TokenInfo
	tokenUsage *types.TokenWithUsage
	err        error
}

func (m *MockAuthService) GetToken() (types.TokenInfo, error) {
	return m.token, m.err
}

func (m *MockAuthService) GetTokenWithUsage() (*types.TokenWithUsage, error) {
	if m.tokenUsage != nil {
		return m.tokenUsage, m.err
	}
	// 如果没有设置 tokenUsage，从 token 构造一个默认的
	if m.err != nil {
		return nil, m.err
	}
	return &types.TokenWithUsage{
		TokenInfo:      m.token,
		AvailableCount: 100, // 测试默认值
	}, nil
}

func TestRespondError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		format         string
		args           []any
		expectedCode   string
		expectedStatus int
	}{
		{
			name:           "BadRequest错误",
			statusCode:     http.StatusBadRequest,
			format:         "无效的请求参数",
			args:           []any{},
			expectedCode:   "bad_request",
			expectedStatus: 400,
		},
		{
			name:           "Unauthorized错误",
			statusCode:     http.StatusUnauthorized,
			format:         "认证失败",
			args:           []any{},
			expectedCode:   "unauthorized",
			expectedStatus: 401,
		},
		{
			name:           "InternalServerError错误",
			statusCode:     http.StatusInternalServerError,
			format:         "服务器内部错误: %v",
			args:           []any{"数据库连接失败"},
			expectedCode:   "internal_error",
			expectedStatus: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			respondError(c, tt.statusCode, tt.format, tt.args...)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]any
			err := json.Unmarshal(w.Body.Bytes(), &response)
			assert.NoError(t, err)

			errorObj, ok := response["error"].(map[string]any)
			assert.True(t, ok, "响应应包含error对象")
			assert.Equal(t, tt.expectedCode, errorObj["code"])
			assert.NotEmpty(t, errorObj["message"])
		})
	}
}

func TestRequestContext_GetTokenAndBody(t *testing.T) {
	tests := []struct {
		name          string
		mockToken     types.TokenInfo
		mockError     error
		requestBody   string
		expectError   bool
		expectedToken types.TokenInfo
	}{
		{
			name: "成功获取token和body",
			mockToken: types.TokenInfo{
				AccessToken: "test-token-123",
			},
			mockError:     nil,
			requestBody:   `{"test": "data"}`,
			expectError:   false,
			expectedToken: types.TokenInfo{AccessToken: "test-token-123"},
		},
		{
			name:        "获取token失败",
			mockToken:   types.TokenInfo{},
			mockError:   assert.AnError,
			requestBody: `{"test": "data"}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			// 设置请求体
			c.Request = httptest.NewRequest("POST", "/test", bytes.NewBufferString(tt.requestBody))

			mockAuth := &MockAuthService{
				token: tt.mockToken,
				err:   tt.mockError,
			}

			reqCtx := &RequestContext{
				GinContext:  c,
				AuthService: mockAuth,
				RequestType: "test",
			}

			tokenInfo, body, err := reqCtx.GetTokenAndBody()

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedToken.AccessToken, tokenInfo.AccessToken)
				assert.NotNil(t, body)
			}
		})
	}
}

func TestRequestContext_GetBody(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/test", bytes.NewBufferString(`{"hello":"world"}`))

	reqCtx := &RequestContext{
		GinContext:  c,
		RequestType: "openai",
	}

	body, err := reqCtx.GetBody()
	assert.NoError(t, err)
	assert.Equal(t, `{"hello":"world"}`, string(body))
}

func TestIsTokenInvalidUpstreamError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		expected   bool
	}{
		{
			name:       "400 + temporarily is suspended 文本",
			statusCode: http.StatusBadRequest,
			body:       `{"message":"Your User ID temporarily is suspended."}`,
			expected:   true,
		},
		{
			name:       "400 + TEMPORARILY_SUSPENDED 代码",
			statusCode: http.StatusBadRequest,
			body:       `{"reason":"TEMPORARILY_SUSPENDED"}`,
			expected:   true,
		},
		{
			name:       "400 普通错误不应判定为token失效",
			statusCode: http.StatusBadRequest,
			body:       `{"message":"invalid request body"}`,
			expected:   false,
		},
		{
			name:       "200 即使包含关键词也不是token失效",
			statusCode: http.StatusOK,
			body:       `{"message":"temporarily is suspended"}`,
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := isTokenInvalidUpstreamError(tt.statusCode, []byte(tt.body))
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestHandleRequestBuildError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	testErr := assert.AnError
	handleRequestBuildError(c, testErr)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	errorObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "internal_error", errorObj["code"])
	assert.Contains(t, errorObj["message"], "构建请求失败")
}

func TestHandleRequestSendError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	testErr := assert.AnError
	handleRequestSendError(c, testErr)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	errorObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "internal_error", errorObj["code"])
	assert.Contains(t, errorObj["message"], "发送请求失败")
}

func TestHandleResponseReadError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	testErr := assert.AnError
	handleResponseReadError(c, testErr)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	errorObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "internal_error", errorObj["code"])
	assert.Contains(t, errorObj["message"], "读取响应体失败")
}

func TestBuildCodeWhispererRequest_IncludesProfileArn(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/test", bytes.NewBufferString(`{}`))

	anthropicReq := types.AnthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 16,
		Messages: []types.AnthropicRequestMessage{
			{Role: "user", Content: "hi"},
		},
	}

	tokenInfo := types.TokenInfo{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		ProfileArn:   "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
	}

	req, err := buildCodeWhispererRequest(c, anthropicReq, tokenInfo, false)
	assert.NoError(t, err)

	bodyBytes, err := io.ReadAll(req.Body)
	assert.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(bodyBytes, &payload)
	assert.NoError(t, err)
	assert.Equal(t, tokenInfo.ProfileArn, payload["profileArn"])
}

// 测试SSE事件发送
func TestAnthropicStreamSender_SendEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	sender := &AnthropicStreamSender{}
	eventData := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_123",
		},
	}

	err := sender.SendEvent(c, eventData)

	assert.NoError(t, err)
	// 验证响应包含SSE格式
	body := w.Body.String()
	assert.Contains(t, body, "event: message_start")
	assert.Contains(t, body, "data:")
}

func TestAnthropicStreamSender_SendError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	sender := &AnthropicStreamSender{}
	testErr := errors.New("test error")

	err := sender.SendError(c, "Test error message", testErr)

	assert.NoError(t, err)
	// 验证响应包含错误信息
	body := w.Body.String()
	assert.Contains(t, body, "error")
	assert.Contains(t, body, "Test error message")
}

func TestOpenAIStreamSender_SendEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	sender := &OpenAIStreamSender{}
	eventData := map[string]any{
		"id": "chatcmpl-123",
		"choices": []any{
			map[string]any{
				"delta": map[string]any{
					"content": "Hello",
				},
			},
		},
	}

	err := sender.SendEvent(c, eventData)

	assert.NoError(t, err)
	// 验证响应包含SSE格式
	body := w.Body.String()
	assert.Contains(t, body, "data:")
}
