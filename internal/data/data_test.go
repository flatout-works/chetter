package data

import (
	"database/sql"
	"testing"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/repositorypostgres"
)

func TestConvertSearchTasksParams(t *testing.T) {
	got := convert[repositorypostgres.SearchTasksParams](repository.SearchTasksParams{
		TeamFilter:        sql.NullString{Valid: true},
		StatusFilter:      "",
		TriggerNameFilter: sql.NullString{Valid: true},
		Search:            "POSTGRESQL",
		Limit:             10,
	})
	if got.TeamFilter != "" || got.StatusFilter != "" || got.TriggerNameFilter != "" {
		t.Fatalf("filters = %#v", got)
	}
	if !got.Search.Valid || got.Search.String != "POSTGRESQL" {
		t.Fatalf("search = %#v", got.Search)
	}
	if got.PageOffset != 0 || got.PageLimit != 10 {
		t.Fatalf("pagination = offset %d limit %d", got.PageOffset, got.PageLimit)
	}
}
