package githubrepo

import (
	"reflect"
	"testing"
)

func TestRepoSlug(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/service.git": "service",
		"https://github.com/org/service":     "service",
		"git@github.com:org/service.git":     "service",
		"ssh://git@github.com/org/service":   "service",
		"https://gitlab.com/group/sub/repo":  "repo",
		"https://example.com/My_Repo":        "my_repo",
		"":                                   "repo",
		"https://github.com/org/":            "org",
	}
	for input, want := range cases {
		if got := RepoSlug(input); got != want {
			t.Errorf("RepoSlug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSecondarySubdirsDeterministic(t *testing.T) {
	urls := []string{
		"https://github.com/acme/shared-lib.git",
		"https://gitlab.com/other/shared-lib.git",
		"git@github.com:acme/tools.git",
	}
	got := SecondarySubdirs(urls)
	want := []string{"repos/shared-lib", "repos/shared-lib-2", "repos/tools"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SecondarySubdirs = %v, want %v", got, want)
	}
	if again := SecondarySubdirs(urls); !reflect.DeepEqual(got, again) {
		t.Fatalf("SecondarySubdirs not deterministic: %v vs %v", got, again)
	}
}

func TestSecondarySubdirsEmpty(t *testing.T) {
	if got := SecondarySubdirs(nil); len(got) != 0 {
		t.Fatalf("SecondarySubdirs(nil) = %v", got)
	}
}
