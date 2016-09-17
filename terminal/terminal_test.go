package terminal

import (
	"testing"
	"runtime"

	"github.com/derekparker/delve/config"
)

func TestSubstitutePath(t *testing.T) {
	type cases []struct {
		rules [][]string
		path  string
		res   string
	}

	casesUnix := cases{
		// Should not depend on separator at the end of rule path
		{[][]string{{"/tmp/path", "/new/path2"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		{[][]string{{"/tmp/path/", "/new/path2/"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		{[][]string{{"/tmp/path/", "/new/path2"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		{[][]string{{"/tmp/path", "/new/path2/"}}, "/tmp/path/file.go", "/new/path2/file.go"},
		// Should apply only for directory names
		{[][]string{{"/tmp/path", "/new/path2"}}, "/tmp/path-2/file.go", "/tmp/path-2/file.go"},
		// First matched rule should be used
		{[][]string{
			{"/tmp/path1", "/new/path1"},
			{"/tmp/path2", "/new/path2"},
			{"/tmp/path2", "/new/path3"}}, "/tmp/path2/file.go", "/new/path2/file.go"},
	}
	casesLinux := cases{
		// Should be case-sensitive
		{[][]string{{"/tmp/path", "/new/path2"}}, "/TmP/path/file.go", "/TmP/path/file.go"},
	}
	casesDarwin := cases{
		// Should be case-insensitive
		{[][]string{{"/tmp/path", "/new/path2"}}, "/TmP/PaTh/file.go", "/new/path2/file.go"},
	}
	casesWindows := cases{
		// Should not depend on separator at the end of rule path
		{[][]string{{"c:\\tmp\\path", "d:\\new\\path2"}}, "c:\\tmp\\path\\file.go", "d:\\new\\path2\\file.go"},
		{[][]string{{"c:\\tmp\\path\\", "d:\\new\\path2\\"}}, "c:\\tmp\\path\\file.go", "d:\\new\\path2\\file.go"},
		{[][]string{{"c:\\tmp\\path", "d:\\new\\path2\\"}}, "c:\\tmp\\path\\file.go", "d:\\new\\path2\\file.go"},
		{[][]string{{"c:\\tmp\\path\\", "d:\\new\\path2"}}, "c:\\tmp\\path\\file.go", "d:\\new\\path2\\file.go"},
		// Should apply only for directory names
		{[][]string{{"c:\\tmp\\path", "d:\\new\\path2"}}, "c:\\tmp\\path-2\\file.go", "c:\\tmp\\path-2\\file.go"},
		// Should be case-insensitive
		{[][]string{{"c:\\tmp\\path", "d:\\new\\path2"}}, "C:\\TmP\\PaTh\\file.go", "d:\\new\\path2\\file.go"},
	}

	var platformCases cases
	if runtime.GOOS == "windows" {
		platformCases = append(platformCases, casesWindows...)
	} else {
		platformCases = append(platformCases, casesUnix...)
		if runtime.GOOS == "darwin" {
			platformCases = append(platformCases, casesDarwin...)
		} else if runtime.GOOS == "linux" {
			platformCases = append(platformCases, casesLinux...)
		}
	}

	for _, c := range(platformCases) {
		var subRules config.SubstitutePathRules
		for _, r := range(c.rules) {
			subRules = append(subRules, config.SubstitutePathRule{From: r[0], To: r[1]})
		}
		res := New(nil, &config.Config{SubstitutePath: subRules}).SubstitutePath(c.path)
		if c.res != res {
			t.Errorf("terminal.SubstitutePath(%q) => %q, want %q", c.path, res, c.res)
		}
	}
}
