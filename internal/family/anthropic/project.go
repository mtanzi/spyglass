package anthropic

import (
	"strings"

	"github.com/tidwall/gjson"

	"github.com/mtanzi/spyglass/internal/core"
)

// maxSystemScan bounds how much system text the project scan reads (ADR 0059).
const maxSystemScan = 32 << 10

// workingDirPrefixes are the line prefixes of Claude Code's environment block
// that name the working directory, in the order they are tried on one line. A
// bullet ("- ") before the prefix is ignored. "Additional working
// directories:" is not here: it lists extra directories, not the project.
var workingDirPrefixes = []string{
	"Primary working directory:",
	"Working directory:",
}

// detectProject returns the project name of a request: the final directory
// name of the working directory that Claude Code puts in its system prompt, or
// "" when the request has none. It reads the system field only, at most the
// first 32 KiB of its text, and stops at the first match. It keeps nothing: the
// caller stores the returned name, which is not the path.
func detectProject(body []byte) string {
	sys := gjson.GetBytes(body, "system")
	var text strings.Builder
	switch {
	case sys.Type == gjson.String:
		text.WriteString(sys.Str)
	case sys.IsArray():
		sys.ForEach(func(_, blk gjson.Result) bool {
			if t := blk.Get("text"); blk.Get("type").String() == "text" && t.Type == gjson.String {
				text.WriteString(t.Str)
				text.WriteByte('\n')
			}
			return text.Len() < maxSystemScan
		})
	default:
		return ""
	}
	s := text.String()
	if len(s) > maxSystemScan {
		// The cut can fall inside a line, and a cut path would give a wrong
		// name, so a line the window does not end is not read.
		s = s[:maxSystemScan]
		if i := strings.LastIndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		} else {
			return ""
		}
	}
	for line := range strings.Lines(s) {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		for _, prefix := range workingDirPrefixes {
			if dir, ok := strings.CutPrefix(line, prefix); ok {
				return projectFromDir(dir)
			}
		}
	}
	return ""
}

// projectFromDir reduces a working directory to its final directory name. Both
// "/" and "\" separate. A name that is not a valid project name, or a path
// with no directory name (the root, a drive root), gives "".
func projectFromDir(dir string) string {
	dir = strings.TrimRight(strings.TrimSpace(dir), `/\`)
	if i := strings.LastIndexAny(dir, `/\`); i >= 0 {
		dir = dir[i+1:]
	}
	if len(dir) == 2 && dir[1] == ':' { // a drive root such as C:\
		return ""
	}
	name, ok := core.ProjectName(dir)
	if !ok {
		return ""
	}
	return name
}
