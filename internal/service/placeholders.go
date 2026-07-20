package service

import (
	"strconv"

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
