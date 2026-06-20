package webapi

import (
	"net/http"
	"testing"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   string
	}{
		{
			name:   "valid bearer token",
			header: http.Header{"Authorization": {"Bearer mytoken123"}},
			want:   "mytoken123",
		},
		{
			name:   "missing authorization header",
			header: http.Header{},
			want:   "",
		},
		{
			name:   "empty authorization",
			header: http.Header{"Authorization": {""}},
			want:   "",
		},
		{
			name:   "not bearer (basic auth)",
			header: http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}},
			want:   "",
		},
		{
			name:   "bearer with extra spaces",
			header: http.Header{"Authorization": {"Bearer   token-with-spaces"}},
			want:   "  token-with-spaces",
		},
		{
			name:   "lowercase bearer",
			header: http.Header{"Authorization": {"bearer token123"}},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bearerToken(tt.header)
			if got != tt.want {
				t.Errorf("bearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}
