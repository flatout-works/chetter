package githubrepo

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "name", input: "Flatout-Works/Chetter", want: "Flatout-Works/Chetter"},
		{name: "https", input: "https://github.com/flatout-works/chetter.git", want: "flatout-works/chetter"},
		{name: "scp ssh", input: "git@github.com:flatout-works/chetter.git", want: "flatout-works/chetter"},
		{name: "ssh URL", input: "ssh://git@github.com/flatout-works/chetter", want: "flatout-works/chetter"},
		{name: "ssh password", input: "ssh://git:secret@github.com/flatout-works/chetter", wantErr: true},
		{name: "trailing slash", input: "https://github.com/flatout-works/chetter/", want: "flatout-works/chetter"},
		{name: "non GitHub", input: "https://gitlab.com/flatout-works/chetter", wantErr: true},
		{name: "HTTP", input: "http://github.com/flatout-works/chetter", wantErr: true},
		{name: "extra path", input: "https://github.com/flatout-works/chetter/issues", wantErr: true},
		{name: "query", input: "https://github.com/flatout-works/chetter?tab=readme", wantErr: true},
		{name: "invalid owner", input: "-owner/repo", wantErr: true},
		{name: "consecutive owner hyphens", input: "bad--owner/repo", wantErr: true},
		{name: "missing owner", input: "repo", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) succeeded: %+v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			if got.FullName() != tt.want {
				t.Fatalf("Parse(%q) = %q, want %q", tt.input, got.FullName(), tt.want)
			}
		})
	}
}

func TestSame(t *testing.T) {
	t.Parallel()
	if !Same("Flatout-Works/Chetter", "https://github.com/flatout-works/chetter.git") {
		t.Fatal("expected repositories to match case-insensitively")
	}
	if Same("flatout-works/chetter", "flatout-works/other") {
		t.Fatal("different repositories matched")
	}
}
