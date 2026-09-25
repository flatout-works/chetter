package store

import (
	"reflect"
	"testing"
)

func TestNormalizeRepoRefsPrimaryFirst(t *testing.T) {
	got := NormalizeRepoRefs([]RepoRef{
		{URL: "https://github.com/acme/lib.git"},
		{URL: "https://github.com/acme/app.git", Ref: "main", Primary: true},
	})
	want := []RepoRef{
		{URL: "https://github.com/acme/app.git", Ref: "main", Primary: true},
		{URL: "https://github.com/acme/lib.git"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeRepoRefs = %+v, want %+v", got, want)
	}
}

func TestNormalizeRepoRefsDefaultsFirstAsPrimary(t *testing.T) {
	got := NormalizeRepoRefs([]RepoRef{
		{URL: "https://github.com/acme/one.git"},
		{URL: "https://github.com/acme/two.git"},
	})
	if len(got) != 2 || !got[0].Primary || got[1].Primary {
		t.Fatalf("expected first entry to be primary: %+v", got)
	}
}

func TestNormalizeRepoRefsDedupesAndDropsEmpty(t *testing.T) {
	got := NormalizeRepoRefs([]RepoRef{
		{URL: "https://github.com/acme/one.git"},
		{URL: "  "},
		{URL: "https://GitHub.com/acme/ONE.git"},
		{URL: "https://github.com/acme/two.git"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %+v", got)
	}
	if got[0].URL != "https://github.com/acme/one.git" || got[1].URL != "https://github.com/acme/two.git" {
		t.Fatalf("unexpected dedupe order: %+v", got)
	}
}

func TestMarshalUnmarshalRepoRefs(t *testing.T) {
	if raw := MarshalRepoRefs(nil); raw != nil {
		t.Fatalf("MarshalRepoRefs(nil) = %q, want nil", raw)
	}
	refs := []RepoRef{
		{URL: "https://github.com/acme/lib.git"},
		{URL: "https://github.com/acme/app.git", Ref: "main", Primary: true},
	}
	raw := MarshalRepoRefs(refs)
	got := UnmarshalRepoRefs(raw)
	want := NormalizeRepoRefs(refs)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if got := UnmarshalRepoRefs([]byte("{not json")); got != nil {
		t.Fatalf("malformed JSON should yield nil, got %+v", got)
	}
}

func TestPrimaryRepoRef(t *testing.T) {
	if _, ok := PrimaryRepoRef(nil); ok {
		t.Fatal("expected no primary for empty set")
	}
	ref, ok := PrimaryRepoRef([]RepoRef{{URL: "https://github.com/acme/app.git", Primary: true}})
	if !ok || ref.URL != "https://github.com/acme/app.git" {
		t.Fatalf("PrimaryRepoRef = %+v ok=%v", ref, ok)
	}
}
