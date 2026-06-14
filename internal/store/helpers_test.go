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
