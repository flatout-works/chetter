package store

import (
	"strings"
	"testing"
)

func TestNormalizeDSN(t *testing.T) {
	t.Parallel()

	// normalizeDSN must force a UTC session time zone on every TiDB/MySQL
	// connection via the time_zone DSN parameter (issue #316). The value is
	// the URL-encoded literal '+00:00' so the go-sql-driver executes
	// `SET time_zone = '+00:00'` on connect.
	const utcTZ = "time_zone=%27%2B00%3A00%27"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "mysql url",
			in:   "mysql://user:pass@example.com:4000/chetter",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true&" + utcTZ,
		},
		{
			name: "tidbcloud url adds tls",
			in:   "mysql://user:pass@gateway01.eu-central-1.prod.aws.tidbcloud.com:4000/chetter",
			want: "user:pass@tcp(gateway01.eu-central-1.prod.aws.tidbcloud.com:4000)/chetter?parseTime=true&" + utcTZ + "&tls=tidb",
		},
		{
			name: "mysql url preserves query",
			in:   "mysql://user:pass@example.com:4000/chetter?tls=true",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true&" + utcTZ + "&tls=true",
		},
		{
			name: "driver dsn adds parse time",
			in:   "user:pass@tcp(example.com:4000)/chetter",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true&" + utcTZ,
		},
		{
			name: "driver dsn preserves parse time",
			in:   "user:pass@tcp(example.com:4000)/chetter?parseTime=true&tls=true",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true&tls=true&" + utcTZ,
		},
		{
			name: "explicit utc time zone preserved",
			in:   "user:pass@tcp(example.com:4000)/chetter?parseTime=true&time_zone=%27%2B00%3A00%27",
			want: "user:pass@tcp(example.com:4000)/chetter?parseTime=true&time_zone=%27%2B00%3A00%27",
		},
		{
			name: "explicit non-utc time zone preserved for preflight",
			in:   "user:pass@tcp(example.com:4000)/chetter?time_zone=SYSTEM",
			want: "user:pass@tcp(example.com:4000)/chetter?time_zone=SYSTEM&parseTime=true",
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

func TestIsUTCTimeZone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session string
		global  string
		want    bool
	}{
		{name: "explicit zero offset", session: "+00:00", global: "+00:00", want: true},
		{name: "negative zero offset", session: "-00:00", global: "SYSTEM", want: true},
		{name: "named utc session", session: "UTC", global: "SYSTEM", want: true},
		{name: "zero offset without minutes", session: "+00", global: "SYSTEM", want: true},
		{name: "system global zero offset", session: "SYSTEM", global: "+00:00", want: true},
		{name: "system global named utc", session: "SYSTEM", global: "UTC", want: true},
		{name: "system global system host tz", session: "SYSTEM", global: "SYSTEM", want: false},
		{name: "system global named tz", session: "SYSTEM", global: "Europe/Vienna", want: false},
		{name: "positive offset", session: "+02:00", global: "SYSTEM", want: false},
		{name: "negative offset", session: "-05:30", global: "SYSTEM", want: false},
		{name: "session named tz", session: "Europe/Stockholm", global: "UTC", want: false},
		{name: "unverifiable empty", session: "", global: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsUTCTimeZone(tt.session, tt.global); got != tt.want {
				t.Errorf("IsUTCTimeZone(%q, %q) = %v, want %v", tt.session, tt.global, got, tt.want)
			}
		})
	}
}

func TestIsUTCValue(t *testing.T) {
	t.Parallel()

	for _, utc := range []string{"UTC", "utc", "GMT", "Z", "ETC/UTC", "+00:00", "-00:00", "+00", "+00:00:00", "-00:00:00"} {
		if !isUTCValue(utc) {
			t.Errorf("isUTCValue(%q) = false, want true", utc)
		}
	}
	for _, nonUTC := range []string{"SYSTEM", "Europe/Vienna", "+02:00", "-05:30", "America/New_York", "", "+00:99"} {
		if isUTCValue(nonUTC) {
			t.Errorf("isUTCValue(%q) = true, want false", nonUTC)
		}
	}
}

// TestOpenFailsClosedOnDialectProbeError verifies that dialect auto-detection
// fails closed: a database that cannot be probed aborts Open instead of
// silently defaulting to TiDB (issue #316).
func TestOpenFailsClosedOnDialectProbeError(t *testing.T) {
	t.Parallel()

	// A unix socket that cannot exist makes the probe fail deterministically
	// without needing a network port.
	dsn := "root@unix(/tmp/chetter-no-such-" + strings.ReplaceAll(t.Name(), "/", "_") + ".sock)/?timeout=500ms"
	st, err := Open(dsn, DialectUnknown)
	if err == nil {
		st.Close()
		t.Fatal("Open() with an unreachable database succeeded; want a fail-closed dialect detection error")
	}
	if !strings.Contains(err.Error(), "detect database dialect") {
		t.Fatalf("Open() error = %v; want a dialect-detection failure", err)
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
