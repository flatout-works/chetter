// Package githubrepo parses GitHub repository names and clone URLs.
package githubrepo

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// Repository is a validated GitHub owner/repository pair.
type Repository struct {
	Owner string
	Name  string
}

func (r Repository) FullName() string {
	return r.Owner + "/" + r.Name
}

func (r Repository) Normalized() string {
	return strings.ToLower(r.FullName())
}

// Parse accepts owner/repo names and HTTPS or SSH github.com clone URLs.
func Parse(value string) (Repository, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Repository{}, fmt.Errorf("GitHub repository is empty")
	}

	name := value
	switch {
	case strings.HasPrefix(strings.ToLower(value), "git@github.com:"):
		name = value[len("git@github.com:"):]
	case strings.Contains(value, "://"):
		u, err := url.Parse(value)
		if err != nil {
			return Repository{}, fmt.Errorf("parse GitHub repository URL: %w", err)
		}
		if u.RawQuery != "" || u.Fragment != "" || u.Port() != "" || !strings.EqualFold(u.Hostname(), "github.com") {
			return Repository{}, fmt.Errorf("repository URL must target github.com without a port, query, or fragment")
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			if u.User != nil {
				return Repository{}, fmt.Errorf("GitHub HTTPS repository URL must not contain credentials")
			}
		case "ssh":
			if u.User == nil || u.User.Username() != "git" {
				return Repository{}, fmt.Errorf("GitHub SSH repository URL must use the git user")
			}
			if _, hasPassword := u.User.Password(); hasPassword {
				return Repository{}, fmt.Errorf("GitHub SSH repository URL must not contain a password")
			}
		default:
			return Repository{}, fmt.Errorf("GitHub repository URL scheme must be https or ssh")
		}
		name = strings.TrimPrefix(u.Path, "/")
	}

	name = strings.TrimSuffix(name, "/")
	name = strings.TrimSuffix(name, ".git")
	parts := strings.Split(name, "/")
	if len(parts) != 2 || !ownerPattern.MatchString(parts[0]) || strings.Contains(parts[0], "--") || !repoPattern.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." {
		return Repository{}, fmt.Errorf("GitHub repository must be a valid owner/repo name")
	}
	return Repository{Owner: parts[0], Name: parts[1]}, nil
}

// Same reports whether two validated repository names identify the same
// case-insensitive GitHub repository.
func Same(left, right string) bool {
	a, err := Parse(left)
	if err != nil {
		return false
	}
	b, err := Parse(right)
	return err == nil && a.Normalized() == b.Normalized()
}
