package auth

import (
	"context"
	"testing"
)

func TestResolveTokenWithoutDatabase(t *testing.T) {
	tests := []struct {
		name       string
		adminToken string
		rawToken   string
		wantOK     bool
		wantAdmin  bool
	}{
		{name: "matching admin token", adminToken: "admin-token", rawToken: "admin-token", wantOK: true, wantAdmin: true},
		{name: "empty raw token", adminToken: "admin-token"},
		{name: "empty tokens"},
		{name: "wrong token", adminToken: "admin-token", rawToken: "wrong"},
		{name: "different length", adminToken: "admin-token", rawToken: "admin-token-extra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope, ok := ResolveToken(context.Background(), tt.adminToken, nil, tt.rawToken)
			if ok != tt.wantOK || scope.Admin != tt.wantAdmin {
				t.Errorf("ResolveToken() = (%+v, %v), want admin=%v ok=%v", scope, ok, tt.wantAdmin, tt.wantOK)
			}
		})
	}
}
