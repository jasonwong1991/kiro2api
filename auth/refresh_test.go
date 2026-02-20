package auth

import (
	"net/http"
	"testing"
)

func TestIsTokenInvalidError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		expected   bool
	}{
		{
			name:       "idc_invalid_grant_with_400_should_be_invalid",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"invalid_grant","error_description":"Invalid token provided"}`,
			expected:   true,
		},
		{
			name:       "non_token_bad_request_should_not_be_invalid",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"invalid_request","error_description":"Missing parameter"}`,
			expected:   false,
		},
		{
			name:       "invalid_token_with_401_should_be_invalid",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":"invalid_token"}`,
			expected:   true,
		},
		{
			name:       "unknown_forbidden_error_should_not_be_invalid",
			statusCode: http.StatusForbidden,
			body:       `{"error":"quota_exceeded"}`,
			expected:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isTokenInvalidError(tc.statusCode, []byte(tc.body))
			if result != tc.expected {
				t.Fatalf("isTokenInvalidError(%d, %s) = %v, want %v", tc.statusCode, tc.body, result, tc.expected)
			}
		})
	}
}
