package locspec

import (
	"runtime"
	"testing"
)

func parseLocationSpecNoError(t *testing.T, locstr string) LocationSpec {
	spec, err := Parse(locstr)
	if err != nil {
		t.Fatalf("Error parsing %q: %v", locstr, err)
	}
	return spec
}

func assertNormalLocationSpec(t *testing.T, locstr string, tgt NormalLocationSpec) {
	spec := parseLocationSpecNoError(t, locstr)

	nls, ok := spec.(*NormalLocationSpec)
	if !ok {
		t.Fatalf("Location %q: expected NormalLocationSpec got %#v", locstr, spec)
	}

	if nls.Base != tgt.Base {
		t.Fatalf("Location %q: expected 'Base' %q got %q", locstr, tgt.Base, nls.Base)
	}

	if nls.LineOffset != tgt.LineOffset {
		t.Fatalf("Location %q: expected 'LineOffset' %d got %d", locstr, tgt.LineOffset, nls.LineOffset)
	}

	if tgt.FuncBase == nil {
		return
	}

	if nls.FuncBase == nil {
		t.Fatalf("Location %q: expected non-nil 'FuncBase'", locstr)
	}

	if *(tgt.FuncBase) != *(nls.FuncBase) {
		t.Fatalf("Location %q: expected 'FuncBase':\n%#v\ngot:\n%#v", locstr, tgt.FuncBase, nls.FuncBase)
	}
}

func TestFunctionLocationParsing(t *testing.T) {
	// Function locations, simple package names, no line offset
	assertNormalLocationSpec(t, "proc.(*Process).Continue", NormalLocationSpec{"proc.(*Process).Continue", &FuncLocationSpec{PackageName: "proc", ReceiverName: "Process", BaseName: "Continue"}, -1})
	assertNormalLocationSpec(t, "proc.Process.Continue", NormalLocationSpec{"proc.Process.Continue", &FuncLocationSpec{PackageName: "proc", ReceiverName: "Process", BaseName: "Continue"}, -1})
	assertNormalLocationSpec(t, "proc.Continue", NormalLocationSpec{"proc.Continue", &FuncLocationSpec{PackageOrReceiverName: "proc", BaseName: "Continue"}, -1})
	assertNormalLocationSpec(t, "(*Process).Continue", NormalLocationSpec{"(*Process).Continue", &FuncLocationSpec{ReceiverName: "Process", BaseName: "Continue"}, -1})
	assertNormalLocationSpec(t, "Continue", NormalLocationSpec{"Continue", &FuncLocationSpec{BaseName: "Continue"}, -1})

	// Function locations, simple package names, line offsets
	assertNormalLocationSpec(t, "proc.(*Process).Continue:10", NormalLocationSpec{"proc.(*Process).Continue", &FuncLocationSpec{PackageName: "proc", ReceiverName: "Process", BaseName: "Continue"}, 10})
	assertNormalLocationSpec(t, "proc.Process.Continue:10", NormalLocationSpec{"proc.Process.Continue", &FuncLocationSpec{PackageName: "proc", ReceiverName: "Process", BaseName: "Continue"}, 10})
	assertNormalLocationSpec(t, "proc.Continue:10", NormalLocationSpec{"proc.Continue", &FuncLocationSpec{PackageOrReceiverName: "proc", BaseName: "Continue"}, 10})
	assertNormalLocationSpec(t, "(*Process).Continue:10", NormalLocationSpec{"(*Process).Continue", &FuncLocationSpec{ReceiverName: "Process", BaseName: "Continue"}, 10})
	assertNormalLocationSpec(t, "Continue:10", NormalLocationSpec{"Continue", &FuncLocationSpec{BaseName: "Continue"}, 10})

	// Function locations, package paths, no line offsets
	assertNormalLocationSpec(t, "github.com/go-delve/delve/pkg/proc.(*Process).Continue", NormalLocationSpec{"github.com/go-delve/delve/pkg/proc.(*Process).Continue", &FuncLocationSpec{PackageName: "github.com/go-delve/delve/pkg/proc", ReceiverName: "Process", BaseName: "Continue"}, -1})
	assertNormalLocationSpec(t, "github.com/go-delve/delve/pkg/proc.Process.Continue", NormalLocationSpec{"github.com/go-delve/delve/pkg/proc.Process.Continue", &FuncLocationSpec{PackageName: "github.com/go-delve/delve/pkg/proc", ReceiverName: "Process", BaseName: "Continue"}, -1})
	assertNormalLocationSpec(t, "github.com/go-delve/delve/pkg/proc.Continue", NormalLocationSpec{"github.com/go-delve/delve/pkg/proc.Continue", &FuncLocationSpec{PackageName: "github.com/go-delve/delve/pkg/proc", BaseName: "Continue"}, -1})

	// Function locations, package paths, line offsets
	assertNormalLocationSpec(t, "github.com/go-delve/delve/pkg/proc.(*Process).Continue:10", NormalLocationSpec{"github.com/go-delve/delve/pkg/proc.(*Process).Continue", &FuncLocationSpec{PackageName: "github.com/go-delve/delve/pkg/proc", ReceiverName: "Process", BaseName: "Continue"}, 10})
	assertNormalLocationSpec(t, "github.com/go-delve/delve/pkg/proc.Process.Continue:10", NormalLocationSpec{"github.com/go-delve/delve/pkg/proc.Process.Continue", &FuncLocationSpec{PackageName: "github.com/go-delve/delve/pkg/proc", ReceiverName: "Process", BaseName: "Continue"}, 10})
	assertNormalLocationSpec(t, "github.com/go-delve/delve/pkg/proc.Continue:10", NormalLocationSpec{"github.com/go-delve/delve/pkg/proc.Continue", &FuncLocationSpec{PackageName: "github.com/go-delve/delve/pkg/proc", BaseName: "Continue"}, 10})
}

func assertSubstitutePathEqual(t *testing.T, expected string, substituted string) {
	if expected != substituted {
		t.Fatalf("Expected substitutedPath to be %s got %s instead", expected, substituted)
	}
}

func TestSubstitutePathUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping unix SubstitutePath test in windows")
	}

	// Relative paths mapping
	assertSubstitutePathEqual(t, "/my/asb/folder/relative/path", SubstitutePath("relative/path", [][2]string{{"", "/my/asb/folder/"}}))
	assertSubstitutePathEqual(t, "/already/abs/path", SubstitutePath("/already/abs/path", [][2]string{{"", "/my/asb/folder/"}}))
	assertSubstitutePathEqual(t, "relative/path", SubstitutePath("/my/asb/folder/relative/path", [][2]string{{"/my/asb/folder/", ""}}))
	assertSubstitutePathEqual(t, "/another/folder/relative/path", SubstitutePath("/another/folder/relative/path", [][2]string{{"/my/asb/folder/", ""}}))
	assertSubstitutePathEqual(t, "my/path", SubstitutePath("relative/path/my/path", [][2]string{{"relative/path", ""}}))
	assertSubstitutePathEqual(t, "/abs/my/path", SubstitutePath("/abs/my/path", [][2]string{{"abs/my", ""}}))

	// Absolute paths mapping
	assertSubstitutePathEqual(t, "/new/mapping/path", SubstitutePath("/original/path", [][2]string{{"/original", "/new/mapping"}}))
	assertSubstitutePathEqual(t, "/no/change/path", SubstitutePath("/no/change/path", [][2]string{{"/original", "/new/mapping"}}))
	assertSubstitutePathEqual(t, "/folder/should_not_be_replaced/path", SubstitutePath("/folder/should_not_be_replaced/path", [][2]string{{"should_not_be_replaced", ""}}))

	// Mix absolute and relative mapping
	assertSubstitutePathEqual(t, "/new/mapping/path", SubstitutePath("/original/path", [][2]string{{"", "/my/asb/folder/"}, {"/my/asb/folder/", ""}, {"/original", "/new/mapping"}}))
	assertSubstitutePathEqual(t, "/my/asb/folder/path", SubstitutePath("path", [][2]string{{"/original", "/new/mapping"}, {"", "/my/asb/folder/"}, {"/my/asb/folder/", ""}}))
	assertSubstitutePathEqual(t, "path", SubstitutePath("/my/asb/folder/path", [][2]string{{"/original", "/new/mapping"}, {"/my/asb/folder/", ""}, {"", "/my/asb/folder/"}}))
}

func TestSubstitutePathWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping windows SubstitutePath test in unix")
	}

	// Relative paths mapping
	assertSubstitutePathEqual(t, "c:\\my\\asb\\folder\\relative\\path", SubstitutePath("relative\\path", [][2]string{{"", "c:\\my\\asb\\folder\\"}}))
	assertSubstitutePathEqual(t, "f:\\already\\abs\\path", SubstitutePath("F:\\already\\abs\\path", [][2]string{{"", "c:\\my\\asb\\folder\\"}}))
	assertSubstitutePathEqual(t, "relative\\path", SubstitutePath("C:\\my\\asb\\folder\\relative\\path", [][2]string{{"c:\\my\\asb\\folder\\", ""}}))
	assertSubstitutePathEqual(t, "f:\\another\\folder\\relative\\path", SubstitutePath("F:\\another\\folder\\relative\\path", [][2]string{{"c:\\my\\asb\\folder\\", ""}}))
	assertSubstitutePathEqual(t, "my\\path", SubstitutePath("relative\\path\\my\\path", [][2]string{{"relative\\path", ""}}))
	assertSubstitutePathEqual(t, "c:\\abs\\my\\path", SubstitutePath("c:\\abs\\my\\path", [][2]string{{"abs\\my", ""}}))

	// Absolute paths mapping
	assertSubstitutePathEqual(t, "c:\\new\\mapping\\path", SubstitutePath("D:\\original\\path", [][2]string{{"d:\\original", "c:\\new\\mapping"}}))
	assertSubstitutePathEqual(t, "f:\\no\\change\\path", SubstitutePath("F:\\no\\change\\path", [][2]string{{"d:\\original", "c:\\new\\mapping"}}))
	assertSubstitutePathEqual(t, "c:\\folder\\should_not_be_replaced\\path", SubstitutePath("c:\\folder\\should_not_be_replaced\\path", [][2]string{{"should_not_be_replaced", ""}}))

	// Mix absolute and relative mapping
	assertSubstitutePathEqual(t, "c:\\new\\mapping\\path", SubstitutePath("D:\\original\\path", [][2]string{{"", "c:\\my\\asb\\folder\\"}, {"c:\\my\\asb\\folder\\", ""}, {"d:\\original", "c:\\new\\mapping"}}))
	assertSubstitutePathEqual(t, "c:\\my\\asb\\folder\\path\\", SubstitutePath("path\\", [][2]string{{"d:\\original", "c:\\new\\mapping"}, {"", "c:\\my\\asb\\folder\\"}, {"c:\\my\\asb\\folder\\", ""}}))
	assertSubstitutePathEqual(t, "path", SubstitutePath("C:\\my\\asb\\folder\\path", [][2]string{{"d:\\original", "c:\\new\\mapping"}, {"c:\\my\\asb\\folder\\", ""}, {"", "c:\\my\\asb\\folder\\"}}))
}
