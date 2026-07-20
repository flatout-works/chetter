package service

import (
	"strconv"
	"strings"

	"github.com/flatout-works/chetter/internal/store"
)

func sqlPlaceholders(dialect store.Dialect, count int) []string {
	placeholders := make([]string, count)
	for i := range count {
		if dialect == store.DialectPostgres {
			placeholders[i] = "$" + strconv.Itoa(i+1)
			continue
		}
		placeholders[i] = "?"
	}
	return placeholders
}

func sqlQuery(dialect store.Dialect, query string) string {
	if dialect != store.DialectPostgres {
		return query
	}
	for _, placeholder := range sqlPlaceholders(dialect, strings.Count(query, "?")) {
		query = strings.Replace(query, "?", placeholder, 1)
	}
	return query
}
