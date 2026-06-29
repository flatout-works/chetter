package webhook

import "testing"

func TestGitHubRepositoryName(t *testing.T) {
	for input, want := range map[string]string{
		"chetter":               "chetter",
		"flatout-works/chetter": "chetter",
		"https://github.com/flatout-works/chetter":     "chetter",
		"https://github.com/flatout-works/chetter.git": "chetter",
		"git@github.com:flatout-works/chetter.git":     "chetter",
	} {
		got, err := githubRepositoryName(input)
		if err != nil {
			t.Fatalf("githubRepositoryName(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("githubRepositoryName(%q) = %q, want %q", input, got, want)
		}
	}

	if _, err := githubRepositoryName(""); err == nil {
		t.Fatal("expected empty repo to fail")
	}
}

func TestRepositoryReadTokenRequest(t *testing.T) {
	req := repositoryReadTokenRequest("chetter")
	repos, ok := req["repositories"].([]string)
	if !ok || len(repos) != 1 || repos[0] != "chetter" {
		t.Fatalf("repositories = %#v, want [chetter]", req["repositories"])
	}
	perms, ok := req["permissions"].(map[string]string)
	if !ok {
		t.Fatalf("permissions = %#v, want map", req["permissions"])
	}
	for _, key := range []string{"contents", "issues", "pull_requests"} {
		if perms[key] != "read" {
			t.Fatalf("permission %s = %q, want read; perms=%#v", key, perms[key], perms)
		}
	}
	for key, value := range perms {
		if value != "read" {
			t.Fatalf("permission %s = %q, want only read permissions", key, value)
		}
	}
}
