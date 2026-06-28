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
