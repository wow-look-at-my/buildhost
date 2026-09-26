package db

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxAptDependsLen caps the stored Depends value.
const MaxAptDependsLen = 1024

var aptRelation = regexp.MustCompile(`^` +
	`[a-z0-9][a-z0-9+.-]+(?::[a-z0-9-]+)?` +
	`(?:[ \t]*\([ \t]*(?:<<|<=|=|>=|>>)[ \t]*[A-Za-z0-9.+~:-]+[ \t]*\))?` +
	`(?:[ \t]*\[[ \t]*!?[a-z0-9-]+(?:[ \t]+!?[a-z0-9-]+)*[ \t]*\])?` +
	`(?:[ \t]*<[ \t]*!?[a-z0-9.+-]+(?:[ \t]+!?[a-z0-9.+-]+)*[ \t]*>)*` +
	`$`)

// ValidateAptDepends checks a Depends value in Debian relationship syntax,
// such as "bubblewrap | docker.io, curl (>= 7.0)". The empty value is valid
// and means no Depends field.
func ValidateAptDepends(s string) error {
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("apt_depends must be one line: a line break would start another control field")
	}
	if len(s) > MaxAptDependsLen {
		return fmt.Errorf("apt_depends is %d bytes, over the %d byte limit", len(s), MaxAptDependsLen)
	}
	for _, clause := range strings.Split(s, ",") {
		for _, alt := range strings.Split(clause, "|") {
			alt = strings.Trim(alt, " \t")
			if alt == "" {
				return fmt.Errorf("apt_depends %q has an empty relation", s)
			}
			if !aptRelation.MatchString(alt) {
				return fmt.Errorf("apt_depends %q: %q is not a Debian relation (want e.g. \"pkg\", \"pkg (>= 1.0)\" or \"a | b\")", s, alt)
			}
		}
	}
	return nil
}
