package service

import "testing"

func TestResolveAgentImageRef(t *testing.T) {
	tests := []struct {
		name   string
		image  string
		prefix string
		want   string
	}{
		{name: "empty", image: "", prefix: "ghcr.io/flatout-works", want: ""},
		{name: "no prefix", image: "chetter-agent:golang", want: "chetter-agent:golang"},
		{name: "prefix local repo tag", image: "chetter-agent:golang", prefix: "ghcr.io/flatout-works", want: "ghcr.io/flatout-works/chetter-agent:golang"},
		{name: "prefix trims slash", image: "chetter-agent:golang", prefix: "ghcr.io/flatout-works/", want: "ghcr.io/flatout-works/chetter-agent:golang"},
		{name: "qualified ghcr unchanged", image: "ghcr.io/flatout-works/chetter-agent:golang", prefix: "registry.example.com/team", want: "ghcr.io/flatout-works/chetter-agent:golang"},
		{name: "qualified localhost unchanged", image: "localhost:5000/chetter-agent:golang", prefix: "ghcr.io/flatout-works", want: "localhost:5000/chetter-agent:golang"},
		{name: "qualified registry port unchanged", image: "registry.example.com:5000/chetter-agent:golang", prefix: "ghcr.io/flatout-works", want: "registry.example.com:5000/chetter-agent:golang"},
		{name: "docker hub namespace is unqualified", image: "flatout/chetter-agent:golang", prefix: "ghcr.io", want: "ghcr.io/flatout/chetter-agent:golang"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAgentImageRef(tt.image, tt.prefix); got != tt.want {
				t.Fatalf("resolveAgentImageRef(%q, %q) = %q, want %q", tt.image, tt.prefix, got, tt.want)
			}
		})
	}
}
