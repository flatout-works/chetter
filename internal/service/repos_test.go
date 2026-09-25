package service

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/flatout-works/chetter/internal/githubrepo"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
)

func rawJSON(t *testing.T, value string) *json.RawMessage {
	t.Helper()
	raw := json.RawMessage(value)
	return &raw
}

func TestNormalizeSubmitRepoRefs(t *testing.T) {
	t.Run("legacy single repo", func(t *testing.T) {
		got := normalizeSubmitRepoRefs(nil, "https://github.com/acme/app.git", "main")
		want := []store.RepoRef{{URL: "https://github.com/acme/app.git", Ref: "main", Primary: true}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("explicit repos win with primary first", func(t *testing.T) {
		got := normalizeSubmitRepoRefs([]store.RepoRef{
			{URL: "https://github.com/acme/lib.git"},
			{URL: "https://github.com/acme/app.git", Primary: true},
		}, "", "")
		if len(got) != 2 || got[0].URL != "https://github.com/acme/app.git" || !got[0].Primary {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("legacy git url prepended when absent from repos", func(t *testing.T) {
		got := normalizeSubmitRepoRefs([]store.RepoRef{
			{URL: "https://github.com/acme/lib.git"},
		}, "https://github.com/acme/app.git", "main")
		if len(got) != 2 || got[0].URL != "https://github.com/acme/app.git" || !got[0].Primary || got[1].URL != "https://github.com/acme/lib.git" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("legacy git url already present is not duplicated", func(t *testing.T) {
		got := normalizeSubmitRepoRefs([]store.RepoRef{
			{URL: "https://github.com/acme/app.git", Primary: true},
			{URL: "https://github.com/acme/lib.git"},
		}, "https://github.com/acme/app.git", "main")
		if len(got) != 2 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestTaskRepoRefsPrefersTaskThenSessionThenLegacy(t *testing.T) {
	task := repository.Task{
		GitUrl: sql.NullString{String: "https://github.com/acme/legacy.git", Valid: true},
		Repos:  rawJSON(t, `[{"url":"https://github.com/acme/app.git","primary":true},{"url":"https://github.com/acme/lib.git"}]`),
	}
	if got := taskRepoRefs(task, repository.AgentSession{}); len(got) != 2 || got[0].URL != "https://github.com/acme/app.git" {
		t.Fatalf("task repos = %+v", got)
	}

	task.Repos = nil
	session := repository.AgentSession{Repos: rawJSON(t, `[{"url":"https://github.com/acme/session.git","primary":true}]`)}
	if got := taskRepoRefs(task, session); len(got) != 1 || got[0].URL != "https://github.com/acme/session.git" {
		t.Fatalf("session repos = %+v", got)
	}

	session.Repos = nil
	got := taskRepoRefs(task, session)
	if len(got) != 1 || got[0].URL != "https://github.com/acme/legacy.git" || !got[0].Primary {
		t.Fatalf("legacy fallback = %+v", got)
	}
}

func TestRepoInTaskRepoSet(t *testing.T) {
	raw := rawJSON(t, `[{"url":"https://github.com/acme/app.git","primary":true},{"url":"https://github.com/other/lib.git"}]`)
	member, err := githubrepo.Parse("other/lib")
	if err != nil {
		t.Fatal(err)
	}
	if !repoInTaskRepoSet(raw, member) {
		t.Fatal("secondary repo should be in the task repo set")
	}
	outsider, err := githubrepo.Parse("evil/repo")
	if err != nil {
		t.Fatal(err)
	}
	if repoInTaskRepoSet(raw, outsider) {
		t.Fatal("repo outside the task set must be rejected")
	}
	if repoInTaskRepoSet(nil, member) {
		t.Fatal("nil repo set must not authorize any repo")
	}
}
