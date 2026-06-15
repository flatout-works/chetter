package store

import (
	"database/sql"
	"testing"
	"time"
)

func TestHostFromDriverDSN(t *testing.T) {
	tests := []struct {
		name, dsn string
		wantHost  string
		wantErr   error
	}{
		{"host with port", "user:pass@tcp(host:4000)/db", "host", nil},
		{"host without port", "user@tcp(myhost)/db", "myhost", nil},
		{"missing tcp", "user:pass@/db", "", errTiDBRequiresTCPHost},
		{"missing closing paren", "user@tcp(myhost/db", "", errTiDBRequiresTCPHost},
		{"empty host", "user@tcp()/db", "", errTiDBRequiresTCPHost},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, err := hostFromDriverDSN(tc.dsn)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Errorf("hostFromDriverDSN(%q) error = %v, want %v", tc.dsn, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tc.wantHost {
				t.Errorf("hostFromDriverDSN(%q) = %q, want %q", tc.dsn, host, tc.wantHost)
			}
		})
	}
}

func TestNullableTime(t *testing.T) {
	t.Run("zero time", func(t *testing.T) {
		got := nullableTime(time.Time{})
		if got != nil {
			t.Errorf("expected nil for zero time, got %v", got)
		}
	})
	t.Run("non-zero time", func(t *testing.T) {
		now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.FixedZone("EST", -5*3600))
		got := nullableTime(now)
		utc, ok := got.(time.Time)
		if !ok {
			t.Fatalf("expected time.Time, got %T", got)
		}
		if utc.Location() != time.UTC {
			t.Errorf("expected UTC, got %v", utc.Location())
		}
	})
}

func TestNullTimePtr(t *testing.T) {
	t.Run("invalid nulltime", func(t *testing.T) {
		got := nullTimePtr(sql.NullTime{Valid: false})
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
	t.Run("valid nulltime", func(t *testing.T) {
		now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.FixedZone("PST", -8*3600))
		got := nullTimePtr(sql.NullTime{Time: now, Valid: true})
		if got == nil {
			t.Fatal("expected non-nil, got nil")
		}
		if (*got).Location() != time.UTC {
			t.Errorf("expected UTC, got %v", (*got).Location())
		}
	})
	t.Run("zero valid time", func(t *testing.T) {
		got := nullTimePtr(sql.NullTime{Time: time.Time{}, Valid: true})
		if got == nil {
			t.Fatal("expected non-nil for valid zero time, got nil")
		}
	})
}

func TestNonNilStrings(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		got := nonNilStrings(nil)
		if got == nil {
			t.Fatal("expected non-nil slice")
		}
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})
	t.Run("empty non-nil", func(t *testing.T) {
		in := []string{}
		got := nonNilStrings(in)
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})
	t.Run("non-empty", func(t *testing.T) {
		in := []string{"a", "b"}
		got := nonNilStrings(in)
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("expected [a b], got %v", got)
		}
	})
}

func TestNonZero(t *testing.T) {
	t.Run("a non-empty uses a", func(t *testing.T) {
		got := NonZero("hello", "world")
		if got != "hello" {
			t.Errorf("expected hello, got %q", got)
		}
	})
	t.Run("a empty uses b", func(t *testing.T) {
		got := NonZero("", "fallback")
		if got != "fallback" {
			t.Errorf("expected fallback, got %q", got)
		}
	})
}

func TestNonZeroInt(t *testing.T) {
	t.Run("a non-zero uses a", func(t *testing.T) {
		got := NonZeroInt(42, 1)
		if got != 42 {
			t.Errorf("expected 42, got %d", got)
		}
	})
	t.Run("a zero uses b", func(t *testing.T) {
		got := NonZeroInt(0, 99)
		if got != 99 {
			t.Errorf("expected 99, got %d", got)
		}
	})
}

func TestNonNilSlice(t *testing.T) {
	t.Run("nil returns b", func(t *testing.T) {
		b := []string{"x", "y"}
		got := NonNilSlice(nil, b)
		if len(got) != 2 || got[0] != "x" {
			t.Errorf("expected [x y], got %v", got)
		}
	})
	t.Run("non-nil returns a", func(t *testing.T) {
		a := []string{"a"}
		b := []string{"x"}
		got := NonNilSlice(a, b)
		if len(got) != 1 || got[0] != "a" {
			t.Errorf("expected [a], got %v", got)
		}
	})
	t.Run("empty a returns a", func(t *testing.T) {
		a := []string{}
		b := []string{"x"}
		got := NonNilSlice(a, b)
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
}

func TestFirstLineOrNA(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		got := firstLineOrNA("")
		if got != "N/A" {
			t.Errorf("expected N/A, got %q", got)
		}
	})
	t.Run("single line", func(t *testing.T) {
		got := firstLineOrNA("hello world")
		if got != "hello world" {
			t.Errorf("expected hello world, got %q", got)
		}
	})
	t.Run("multi-line", func(t *testing.T) {
		got := firstLineOrNA("first line\nsecond line")
		if got != "first line" {
			t.Errorf("expected first line, got %q", got)
		}
	})
	t.Run("truncates long lines", func(t *testing.T) {
		long := string(make([]byte, 300))
		got := firstLineOrNA(long)
		if len(got) != 200 {
			t.Errorf("expected 200 chars, got %d", len(got))
		}
	})
}

func TestCurrentTaskIDsFromMetadata(t *testing.T) {
	t.Run("nil data", func(t *testing.T) {
		got := currentTaskIDsFromMetadata(nil)
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
	t.Run("empty data", func(t *testing.T) {
		got := currentTaskIDsFromMetadata([]byte{})
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		got := currentTaskIDsFromMetadata([]byte(`not json`))
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
	t.Run("valid with task ids", func(t *testing.T) {
		data := []byte(`{"current_task_ids": ["task_1", "task_2"]}`)
		got := currentTaskIDsFromMetadata(data)
		if len(got) != 2 || got[0] != "task_1" || got[1] != "task_2" {
			t.Errorf("expected [task_1 task_2], got %v", got)
		}
	})
	t.Run("valid without task ids", func(t *testing.T) {
		data := []byte(`{"status": "active"}`)
		got := currentTaskIDsFromMetadata(data)
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
}
