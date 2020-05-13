package locspec

import (
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
