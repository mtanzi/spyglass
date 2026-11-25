package core

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxProjectLen bounds a project name in characters (ADR 0059).
const MaxProjectLen = 128

// ProjectName returns s trimmed and reports whether it is a valid project
// name: not empty, at most MaxProjectLen characters, and printable characters
// only. The header, the key config and the extractor share this rule, so a
// project name is the same kind of value wherever it comes from.
func ProjectName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxProjectLen {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return s, true
}
