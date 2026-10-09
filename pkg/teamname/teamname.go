// Package teamname defines and validates the canonical shape of Chetter
// team names.
//
// A team name doubles as a directory name in the definitions repository
// (`groups/<name>/`), so it must be safe to embed in a relative path and in
// shells, URLs, and logs. The canonical shape is lowercase-with-dashes, e.g.
// "chetter-core".
//
// The same helper is used by the create path (Service.CreateTeam /
// chetter_create_team) and the definitions sync path (pkg/definitions), so a
// hand-created groups/<name>/ directory is rejected even when no team was
// created through the API.
package teamname

import (
	"fmt"
	"regexp"
)

// MaxLen is the maximum accepted team-name length. It mirrors the
// `teams.name` column type, VARCHAR(128).
const MaxLen = 128

// Pattern is the regular expression that every team name must match:
// one or more lowercase alphanumeric segments joined by single dashes.
const Pattern = `^[a-z0-9]+(-[a-z0-9]+)*$`

var pattern = regexp.MustCompile(Pattern)

// IsValid reports whether name matches the canonical lowercase-with-dashes
// rule and is within the length limit.
func IsValid(name string) bool {
	return len(name) <= MaxLen && pattern.MatchString(name)
}

// Validate returns a nil error when name is a canonical team name, and an
// actionable error naming the rule otherwise.
func Validate(name string) error {
	if name == "" {
		return fmt.Errorf("team name is required")
	}
	if len(name) > MaxLen {
		return fmt.Errorf("team name %q is too long (max %d characters)", name, MaxLen)
	}
	if !pattern.MatchString(name) {
		return fmt.Errorf("team name %q must be lowercase-with-dashes matching %s (for example %q)",
			name, Pattern, "chetter-core")
	}
	return nil
}
