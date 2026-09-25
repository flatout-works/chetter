package githubrepo

import (
	"strings"
	"unicode"
)

// ReposSubdir is the parent directory (relative to the workspace root) that
// holds the checkouts of secondary repositories.
const ReposSubdir = "repos"

// SecondarySubdirs returns the deterministic, workspace-relative
// subdirectory for each secondary repository in list order.
//
// The base name is derived from the repository URL (owner/repo -> repo) and
// sanitized to a safe path segment. Collisions are resolved in list order by
// appending -2, -3, ... so the same ordered repo set always produces the same
// layout across retries, resumes, and replicas.
func SecondarySubdirs(urls []string) []string {
	used := make(map[string]bool, len(urls))
	out := make([]string, len(urls))
	for i, raw := range urls {
		base := RepoSlug(raw)
		candidate := base
		for n := 2; used[candidate]; n++ {
			candidate = base + "-" + itoa(n)
		}
		used[candidate] = true
		out[i] = ReposSubdir + "/" + candidate
	}
	return out
}

// RepoSlug returns a safe directory name for a repository URL. It never
// returns an empty string.
func RepoSlug(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.TrimRight(value, "/")
	value = strings.TrimSuffix(value, ".git")
	// Strip the scheme (https://, ssh://, git://, file://, ...).
	if idx := strings.Index(value, "://"); idx >= 0 {
		value = value[idx+len("://"):]
	}
	// Strip scp-like user@host: from "git@github.com:owner/repo".
	if at := strings.LastIndex(value, "@"); at >= 0 {
		rest := value[at+1:]
		if strings.ContainsAny(rest, "/:") {
			value = rest
		}
	}
	if idx := strings.IndexAny(value, "/:"); idx >= 0 {
		value = value[idx+1:]
	}
	// Keep only the final path segment.
	if idx := strings.LastIndexAny(value, "/:"); idx >= 0 {
		value = value[idx+1:]
	}
	slug := sanitizeSegment(value)
	if slug == "" {
		return "repo"
	}
	return slug
}

func sanitizeSegment(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), ".-")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
