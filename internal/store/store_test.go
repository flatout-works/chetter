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
			in:   "mysql://user:pass@example.com:4000/chetter",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true",
		},
		{
			name: "tidbcloud url adds tls",
			in:   "mysql://user:pass@gateway01.eu-central-1.prod.aws.tidbcloud.com:4000/chetter",
			want: "user:pass@tcp(gateway01.eu-central-1.prod.aws.tidbcloud.com:4000)/chetter?parseTime=true&tls=tidb",
		},
		{
			name: "mysql url preserves query",
			in:   "mysql://user:pass@example.com:4000/chetter?tls=true",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true&tls=true",
		},
		{
			name: "driver dsn adds parse time",
			in:   "user:pass@tcp(example.com:4000)/chetter",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true",
		},
		{
			name: "driver dsn preserves parse time",
			in:   "user:pass@tcp(example.com:4000)/chetter?parseTime=true&tls=true",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true&tls=true",
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

func TestParseDialect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  Dialect
	}{
		{input: "tidb", want: DialectTiDB},
		{input: "mysql", want: DialectMySQL},
		{input: "postgres", want: DialectPostgres},
		{input: "PostgreSQL", want: DialectPostgres},
		{input: "unknown", want: DialectUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := ParseDialect(tt.input); got != tt.want {
				t.Fatalf("ParseDialect(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPostgresDSN(t *testing.T) {
	t.Parallel()

	for _, dsn := range []string{
		"postgres://user:pass@localhost:5432/chetter",
		"postgresql://user:pass@localhost:5432/chetter",
	} {
		if !isPostgresDSN(dsn) {
			t.Errorf("expected postgres DSN %q to be detected", dsn)
		}
	}
	if isPostgresDSN("root@tcp(localhost:4000)/chetter") {
		t.Error("unexpected PostgreSQL detection for MySQL DSN")
	}
}

func TestNormalizePostgresDSN(t *testing.T) {
	t.Parallel()

	if got := normalizePostgresDSN("postgres://user:pass@localhost:5432/chetter?sslmode=disable"); got != "postgres://user:pass@localhost:5432/chetter?sslmode=disable&timezone=UTC" {
		t.Fatalf("normalizePostgresDSN() = %q", got)
	}
	if got := normalizePostgresDSN("postgres://user:pass@localhost:5432/chetter?timezone=Europe%2FStockholm"); got != "postgres://user:pass@localhost:5432/chetter?timezone=Europe%2FStockholm" {
		t.Fatalf("normalizePostgresDSN() changed explicit timezone: %q", got)
	}
}

func TestContainerLimitsFromMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		metadata  []byte
		wantMemMB int
		wantCPU   float64
	}{
		{name: "empty metadata", metadata: nil, wantMemMB: 0, wantCPU: 0},
		{name: "no limits", metadata: []byte(`{"runner_id":"r1","status":"active"}`), wantMemMB: 0, wantCPU: 0},
		{name: "limits present", metadata: []byte(`{"runner_id":"r1","container_memory_mb":1024,"container_cpu":2}`), wantMemMB: 1024, wantCPU: 2},
		{name: "partial limits", metadata: []byte(`{"runner_id":"r1","container_cpu":1.5}`), wantMemMB: 0, wantCPU: 1.5},
		{name: "invalid json", metadata: []byte(`not json`), wantMemMB: 0, wantCPU: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			memMB, cpu := containerLimitsFromMetadata(tc.metadata)
			if memMB != tc.wantMemMB {
				t.Errorf("containerLimitsFromMetadata memory = %d, want %d", memMB, tc.wantMemMB)
			}
			if cpu != tc.wantCPU {
				t.Errorf("containerLimitsFromMetadata cpu = %v, want %v", cpu, tc.wantCPU)
			}
		})
	}
}
