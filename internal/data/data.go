// Package data selects the generated repository for the active SQL dialect.
package data

import (
	"context"
	"database/sql"
	"reflect"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/repositorypostgres"
	"github.com/flatout-works/chetter/internal/store"
)

//go:generate go run ./cmd/genfacade

// New returns the generated repository matching dialect. Its public types are
// always repository types so callers do not need database-specific conversions.
func New(db *sql.DB, dialect store.Dialect) Repository {
	if dialect == store.DialectPostgres {
		return &Queries{postgres: repositorypostgres.New(db), db: db}
	}
	return repository.New(db)
}

// NewTx returns a repository bound to tx for transaction retries.
func NewTx(tx *sql.Tx, dialect store.Dialect) Repository {
	if dialect == store.DialectPostgres {
		return &Queries{postgres: repositorypostgres.New(tx)}
	}
	return repository.New(tx)
}

// Queries delegates PostgreSQL operations to repositorypostgres. The generated
// facade methods retain repository's model and parameter types for callers.
type Queries struct {
	postgres *repositorypostgres.Queries
	db       *sql.DB
}

// DB exposes the pool when this facade was constructed from one.
func (q *Queries) DB() *sql.DB { return q.db }

// SearchTasks has PostgreSQL-specific nullable filters and pagination names.
// Keep that generated difference isolated here while callers use repository's
// established parameter type.
func (q *Queries) SearchTasks(ctx context.Context, arg repository.SearchTasksParams) ([]repository.ChetterTask, error) {
	search := sql.NullString{}
	switch value := arg.Search.(type) {
	case string:
		search = sql.NullString{String: value, Valid: true}
	case sql.NullString:
		search = value
	}
	value, err := q.postgres.SearchTasks(ctx, repositorypostgres.SearchTasksParams{
		TeamFilter:        nullableStringValue(arg.TeamFilter),
		StatusFilter:      arg.StatusFilter,
		TriggerNameFilter: nullableStringValue(arg.TriggerNameFilter),
		Search:            search,
		PageOffset:        arg.Offset,
		PageLimit:         arg.Limit,
	})
	return convert[[]repository.ChetterTask](value), err
}

// ListAuditLog consolidates sqlc's duplicated MySQL filter parameters into the
// PostgreSQL query's single parameter per filter.
func (q *Queries) ListAuditLog(ctx context.Context, arg repository.ListAuditLogParams) ([]repository.ListAuditLogRow, error) {
	createdAfter, _ := arg.Column14.(sql.NullTime)
	value, err := q.postgres.ListAuditLog(ctx, repositorypostgres.ListAuditLogParams{
		EventTypeFilter:  arg.EventType,
		SourceTypeFilter: optionalString(arg.SourceType),
		SourceIDFilter:   optionalString(arg.SourceID),
		TargetTypeFilter: optionalString(arg.TargetType),
		TargetIDFilter:   optionalString(arg.TargetID),
		RepoFilter:       optionalString(arg.Repo),
		CreatedAfter:     createdAfter,
		PageOffset:       arg.Offset,
		PageLimit:        arg.Limit,
	})
	return convert[[]repository.ListAuditLogRow](value), err
}

func (q *Queries) ListTaskArtifacts(ctx context.Context, arg repository.ListTaskArtifactsParams) ([]repository.ListTaskArtifactsRow, error) {
	value, err := q.postgres.ListTaskArtifacts(ctx, repositorypostgres.ListTaskArtifactsParams{
		TaskID:         arg.TaskID,
		AgentSessionID: optionalString(arg.AgentSessionID),
		ArtifactType:   arg.ArtifactType,
		Repo:           arg.Repo,
		PageOffset:     arg.Offset,
		PageLimit:      arg.Limit,
	})
	return convert[[]repository.ListTaskArtifactsRow](value), err
}

func nullableStringValue(value sql.NullString) interface{} {
	return value.String
}

func optionalString(value sql.NullString) sql.NullString {
	return sql.NullString{String: value.String, Valid: true}
}

func convert[T any](source any) T {
	var destination T
	copyValue(reflect.ValueOf(&destination).Elem(), reflect.ValueOf(source))
	return destination
}

func copyValue(destination, source reflect.Value) {
	if !source.IsValid() {
		return
	}
	for source.Kind() == reflect.Interface {
		if source.IsNil() {
			return
		}
		source = source.Elem()
	}
	if destination.Kind() == reflect.Interface && source.Type() == reflect.TypeFor[sql.NullString]() {
		destination.Set(reflect.ValueOf(source.FieldByName("String").String()))
		return
	}
	if source.Type().AssignableTo(destination.Type()) {
		destination.Set(source)
		return
	}
	if source.Type().ConvertibleTo(destination.Type()) {
		destination.Set(source.Convert(destination.Type()))
		return
	}

	switch destination.Kind() {
	case reflect.Pointer:
		if source.Kind() == reflect.Pointer && source.IsNil() {
			return
		}
		destination.Set(reflect.New(destination.Type().Elem()))
		copyValue(destination.Elem(), source)
	case reflect.Struct:
		if source.Kind() == reflect.String && destination.Type() == reflect.TypeFor[sql.NullString]() {
			destination.Set(reflect.ValueOf(sql.NullString{String: source.String(), Valid: true}))
			return
		}
		for i := range destination.NumField() {
			field := destination.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			value := sourceField(source, field.Name)
			if value.IsValid() {
				copyValue(destination.Field(i), value)
			}
		}
	case reflect.Slice:
		if source.Kind() != reflect.Slice {
			return
		}
		destination.Set(reflect.MakeSlice(destination.Type(), source.Len(), source.Len()))
		for i := range source.Len() {
			copyValue(destination.Index(i), source.Index(i))
		}
	case reflect.String:
		if source.Type() == reflect.TypeFor[sql.NullString]() {
			destination.SetString(source.FieldByName("String").String())
		}
	case reflect.Interface:
		if source.Type().AssignableTo(destination.Type()) || source.Type().Implements(destination.Type()) {
			destination.Set(source)
		}
	}
}

func sourceField(source reflect.Value, name string) reflect.Value {
	if source.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	if field := source.FieldByName(name); field.IsValid() {
		return field
	}
	switch name {
	case "PageOffset":
		return source.FieldByName("Offset")
	case "PageLimit":
		return source.FieldByName("Limit")
	case "DefinitionTypeFilter":
		return source.FieldByName("Column1")
	case "SourceIDFilter":
		if source.FieldByName("DefinitionType").IsValid() {
			return source.FieldByName("Column3")
		}
		return source.FieldByName("Column1")
	case "StatusFilter":
		return source.FieldByName("Column3")
	}
	return reflect.Value{}
}
