// Package migrations embeds the PostgreSQL Goose migrations for startup use.
package migrations

import "embed"

// Files contains every PostgreSQL migration shipped with the server binary.
// Keeping startup migrations embedded makes local binaries and container images
// follow the same upgrade path.
//
//go:embed *.sql
var Files embed.FS
