package embedded

import (
	"testing"
)

func TestStartAndClose(t *testing.T) {
	srv, err := Start()
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if srv.Port == 0 {
		t.Error("expected non-zero port")
	}
	if srv.URL == "" {
		t.Error("expected non-empty URL")
	}
	if srv.ClientURL() != srv.URL {
		t.Errorf("ClientURL() = %q, want %q", srv.ClientURL(), srv.URL)
	}
	srv.Close()
}
