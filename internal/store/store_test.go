package store

import "testing"

func TestNormalizeDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "mysql url",
			in:   "mysql://user:pass@example.com:4000/devfleet",
			want: "user:pass@tcp(example.com:4000)/devfleet?parseTime=true",
		},
		{
			name: "tidbcloud url adds tls",
			in:   "mysql://user:pass@gateway01.eu-central-1.prod.aws.tidbcloud.com:4000/devfleet",
			want: "user:pass@tcp(gateway01.eu-central-1.prod.aws.tidbcloud.com:4000)/devfleet?parseTime=true&tls=tidb",
		},
		{
			name: "mysql url preserves query",
			in:   "mysql://user:pass@example.com:4000/devfleet?tls=true",
			want: "user:pass@tcp(example.com:4000)/devfleet?parseTime=true&tls=true",
		},
		{
			name: "driver dsn adds parse time",
			in:   "user:pass@tcp(example.com:4000)/devfleet",
			want: "user:pass@tcp(example.com:4000)/devfleet?parseTime=true",
		},
		{
			name: "driver dsn preserves parse time",
			in:   "user:pass@tcp(example.com:4000)/devfleet?parseTime=true&tls=true",
			want: "user:pass@tcp(example.com:4000)/devfleet?parseTime=true&tls=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeDSN(tt.in); got != tt.want {
				t.Fatalf("normalizeDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}
