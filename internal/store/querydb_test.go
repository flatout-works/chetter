package store

import "testing"

func TestPostgresQuery(t *testing.T) {
	t.Parallel()

	t.Run("rebinds interval placeholder", func(t *testing.T) {
		got := postgresQuery("SELECT * FROM runners WHERE id = ? AND seen_at > DATE_SUB(NOW(), INTERVAL ? SECOND)")
		want := "SELECT * FROM runners WHERE id = $1 AND seen_at > NOW() - ($2 * INTERVAL '1 second')"
		if got != want {
			t.Fatalf("postgresQuery() = %q, want %q", got, want)
		}
	})

	t.Run("converts insert ignore after sqlc comment", func(t *testing.T) {
		got := postgresQuery("-- name: Insert :exec\nINSERT IGNORE INTO things (id) VALUES (?)")
		want := "-- name: Insert :exec\nINSERT INTO things (id) VALUES ($1) ON CONFLICT DO NOTHING"
		if got != want {
			t.Fatalf("postgresQuery() = %q, want %q", got, want)
		}
	})
}
