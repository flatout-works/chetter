package network

import "testing"

func TestNarrowListenAddr(t *testing.T) {
	tests := []struct {
		name       string
		listenAddr string
		host       string
		want       string
		wantErr    bool
	}{
		{name: "empty host", listenAddr: ":18081", host: "10.0.0.8", want: "10.0.0.8:18081"},
		{name: "IPv4 wildcard", listenAddr: "0.0.0.0:18081", host: "10.0.0.8", want: "10.0.0.8:18081"},
		{name: "IPv6 wildcard", listenAddr: "[::]:18081", host: "10.0.0.8", want: "10.0.0.8:18081"},
		{name: "explicit host", listenAddr: "127.0.0.1:18081", host: "10.0.0.8", want: "127.0.0.1:18081"},
		{name: "invalid replacement", listenAddr: ":18081", host: "runner.local", wantErr: true},
		{name: "invalid listen address", listenAddr: "18081", host: "10.0.0.8", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NarrowListenAddr(tt.listenAddr, tt.host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NarrowListenAddr() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NarrowListenAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}
