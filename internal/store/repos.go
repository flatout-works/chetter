package store

import (
	"encoding/json"
	"strings"
)

// RepoRef is one repository in a task's repository set. Primary marks the
// repository cloned at the workspace root; it keeps the historical single-repo
// contract (GitHub-triggered provenance, artifact correlation, and the default
// target of the runner GitHub MCP tools). Secondary repositories are cloned
// into deterministic subdirectories. See issue #434.
type RepoRef struct {
	URL     string `json:"url"`
	Ref     string `json:"ref,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// NormalizeRepoRefs trims, drops empties, de-duplicates by URL (case
// insensitive), and orders the set so the primary repository is first.
// Exactly one entry is primary: the first entry flagged primary, or the first
// entry when none is flagged. The returned slice is nil when no usable repo
// remains.
func NormalizeRepoRefs(in []RepoRef) []RepoRef {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	deduped := make([]RepoRef, 0, len(in))
	primaryIndex := -1
	for _, ref := range in {
		ref.URL = strings.TrimSpace(ref.URL)
		ref.Ref = strings.TrimSpace(ref.Ref)
		if ref.URL == "" {
			continue
		}
		key := strings.ToLower(ref.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		if ref.Primary && primaryIndex < 0 {
			primaryIndex = len(deduped)
		}
		deduped = append(deduped, ref)
	}
	if len(deduped) == 0 {
		return nil
	}
	if primaryIndex < 0 {
		primaryIndex = 0
	}
	ordered := make([]RepoRef, 0, len(deduped))
	ordered = append(ordered, RepoRef{URL: deduped[primaryIndex].URL, Ref: deduped[primaryIndex].Ref, Primary: true})
	for i, ref := range deduped {
		if i == primaryIndex {
			continue
		}
		ordered = append(ordered, RepoRef{URL: ref.URL, Ref: ref.Ref})
	}
	return ordered
}

// PrimaryRepoRef returns the primary entry of a normalized repo set.
func PrimaryRepoRef(refs []RepoRef) (RepoRef, bool) {
	if len(refs) == 0 {
		return RepoRef{}, false
	}
	if refs[0].URL == "" {
		return RepoRef{}, false
	}
	return refs[0], true
}

// MarshalRepoRefs serializes a repo set for storage. It returns nil for an
// empty set so the column is stored as NULL.
func MarshalRepoRefs(refs []RepoRef) []byte {
	normalized := NormalizeRepoRefs(refs)
	if len(normalized) == 0 {
		return nil
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil
	}
	return raw
}

// UnmarshalRepoRefs parses a stored repo set. Malformed JSON yields an empty
// set rather than an error so legacy rows never break list/detail reads.
func UnmarshalRepoRefs(raw []byte) []RepoRef {
	if len(raw) == 0 {
		return nil
	}
	var refs []RepoRef
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil
	}
	return NormalizeRepoRefs(refs)
}
