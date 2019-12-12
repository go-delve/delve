package debug

import "github.com/go-delve/delve/pkg/proc"

const (
	// UnrecoveredPanic is the name given to the unrecovered panic breakpoint.
	UnrecoveredPanic = "unrecovered-panic"

	// FatalThrow is the name given to the breakpoint triggered when the target process dies because of a fatal runtime error
	FatalThrow = "runtime-fatal-throw"

	unrecoveredPanicID = -1
	fatalThrowID       = -2
)

// createUnrecoveredPanicBreakpoint creates the unrecoverable-panic breakpoint.
// This function is meant to be called by implementations of the Process interface.
func createUnrecoveredPanicBreakpoint(t *Target) {
	panicpcs, err := t.BinInfo().FindFunctionLocation(t, t.Breakpoints(), "runtime.startpanic", 0)
	if _, isFnNotFound := err.(*proc.ErrFunctionNotFound); isFnNotFound {
		panicpcs, err = t.BinInfo().FindFunctionLocation(t, t.Breakpoints(), "runtime.fatalpanic", 0)
	}
	if err == nil {
		bp, err := t.Breakpoints().SetWithID(unrecoveredPanicID, panicpcs[0], t.Process.WriteBreakpoint)
		if err == nil {
			bp.Name = UnrecoveredPanic
			bp.Variables = []string{"runtime.curg._panic.arg"}
		}
	}
}

func createFatalThrowBreakpoint(t *Target) {
	fatalpcs, err := t.BinInfo().FindFunctionLocation(t, t.Breakpoints(), "runtime.fatalthrow", 0)
	if err == nil {
		bp, err := t.Breakpoints().SetWithID(fatalThrowID, fatalpcs[0], t.Process.WriteBreakpoint)
		if err == nil {
			bp.Name = FatalThrow
		}
	}
}
