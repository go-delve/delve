package terminal

import (
	"errors"
	"net/rpc"
	"runtime"
	"testing"

	"github.com/go-delve/delve/pkg/config"
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
		// Should apply to directory prefixes
		{[]tRule{{"/tmp/path", "/new/path2"}}, "/tmp/path-2/file.go", "/tmp/path-2/file.go"},
		// Should apply to exact matches
		{[]tRule{{"/tmp/path/file.go", "/new/path2/file2.go"}}, "/tmp/path/file.go", "/new/path2/file2.go"},
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
	casesFreebsd := []tCase{
		// Should be case-sensitive
		{[]tRule{{"/tmp/path", "/new/path2"}}, "/TmP/path/file.go", "/TmP/path/file.go"},
	}
	casesDarwin := []tCase{
		// Can be either case-sensitive or case-insensitive depending on
		// filesystem settings, we always treat it as case-sensitive.
		{[]tRule{{"/tmp/path", "/new/path2"}}, "/TmP/PaTh/file.go", "/TmP/PaTh/file.go"},
	}
	casesWindows := []tCase{
		// Should not depend on separator at the end of rule path
		{[]tRule{{`c:\tmp\path`, `d:\new\path2`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		{[]tRule{{`c:\tmp\path\`, `d:\new\path2\`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		{[]tRule{{`c:\tmp\path`, `d:\new\path2\`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		{[]tRule{{`c:\tmp\path\`, `d:\new\path2`}}, `c:\tmp\path\file.go`, `d:\new\path2\file.go`},
		// Should apply to directory prefixes
		{[]tRule{{`c:\tmp\path`, `d:\new\path2`}}, `c:\tmp\path-2\file.go`, `c:\tmp\path-2\file.go`},
		// Should apply to exact matches
		{[]tRule{{`c:\tmp\path\file.go`, `d:\new\path2\file2.go`}}, `c:\tmp\path\file.go`, `d:\new\path2\file2.go`},
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
	if runtime.GOOS == "freebsd" {
		return append(casesUnix, casesFreebsd...)
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

func TestIsErrProcessExited(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		result bool
	}{
		{"empty error", errors.New(""), false},
		{"non-ServerError", errors.New("Process 33122 has exited with status 0"), false},
		{"ServerError with zero status", rpc.ServerError("Process 33122 has exited with status 0"), true},
		{"ServerError with non-zero status", rpc.ServerError("Process 2 has exited with status 25"), true},
	}
	for _, test := range tests {
		if isErrProcessExited(test.err) != test.result {
			t.Error(test.name)
		}
	}
}
