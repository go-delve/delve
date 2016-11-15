package terminal

import (
	"runtime"
	"testing"

	"github.com/derekparker/delve/config"
)

type tRule struct {
	from string
	to   string
}

type tCase struct {
	rules []tRule
	path  string
	res   string
}

func platformCases() []tCase {
	casesUnix := []tCase{
		// Should not depend on separator at the end of rule path
		{[]tRule{{"/tmp/path", "/new/path2"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		{[]tRule{{"/tmp/path/", "/new/path2/"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		{[]tRule{{"/tmp/path/", "/new/path2"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		{[]tRule{{"/tmp/path", "/new/path2/"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		// Should apply only for directory names
		{[]tRule{{"/tmp/path", "/new/path2"}}, "/tmp/path-2/file.go", "/tmp/path-2/file.go"},
		// First matched rule should be used
		{[]tRule{
			{"/tmp/path1", "/new/path1"},
			{"/tmp/path2", "/new/path2"},
			{"/tmp/path2", "/new/path3"}}, "/tmp/path2/file.go", "/new/path2/file.go"},
	}
	casesLinux := []tCase{
		// Should be case-sensitive
		{[]tRule{{"/tmp/path", "/new/path2"}}, "/TmP/path/file.go", "/TmP/path/file.go"},
	}
	casesDarwin := []tCase{
		// Should be case-insensitive
		{[]tRule{{"/tmp/path", "/new/path2"}}, "/TmP/PaTh/file.go", "/new/path2/file.go"},
	}
	casesWindows := []tCase{
		// Should not depend on separator at the end of rule path
		{[]tRule{{`c:\tmp\path`, `d:\new\path2`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		{[]tRule{{`c:\tmp\path\`, `d:\new\path2\`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		{[]tRule{{`c:\tmp\path`, `d:\new\path2\`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		{[]tRule{{`c:\tmp\path\`, `d:\new\path2`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		// Should apply only for directory names
		{[]tRule{{`c:\tmp\path`, `d:\new\path2`}}, `c:\tmp\path-2\file.go`, `c:\tmp\path-2\file.go`},
		// Should be case-insensitive
		{[]tRule{{`c:\tmp\path`, `d:\new\path2`}}, `C:\TmP\PaTh\file.go`, `d:\new\path2\file.go`},
	}

	if runtime.GOOS == "windows" {
		return casesWindows
	}
	if runtime.GOOS == "darwin" {
		return append(casesUnix, casesDarwin...)
	}
	if runtime.GOOS == "linux" {
		return append(casesUnix, casesLinux...)
	}
	return casesUnix
}

func TestSubstitutePath(t *testing.T) {
	for _, c := range platformCases() {
		var subRules config.SubstitutePathRules
		for _, r := range c.rules {
			subRules = append(subRules, config.SubstitutePathRule{From: r.from, To: r.to})
		}
		res := New(nil, &config.Config{SubstitutePath: subRules}).substitutePath(c.path)
		if c.res != res {
			t.Errorf("terminal.SubstitutePath(%q) => %q, want %q", c.path, res, c.res)
		}
	}
}
