package proc

import (
	"fmt"
	"strings"
)

// TargetGroup reperesents a group of target processes being debugged that
// will be resumed and stopped simultaneously.
// New targets are automatically added to the group if exec catching is
// enabled and the backend supports it, otherwise the group will always
// contain a single target process.
type TargetGroup struct {
	targets  []*Target
	Selected *Target

	RecordingManipulation
	recman RecordingManipulationInternal

	// KeepSteppingBreakpoints determines whether certain stop reasons (e.g. manual halts)
	// will keep the stepping breakpoints instead of clearing them.
	KeepSteppingBreakpoints KeepSteppingBreakpoints

	LogicalBreakpoints map[int]*LogicalBreakpoint

	continueOnce ContinueOnceFunc
	cctx         *ContinueOnceContext
}

// NewGroup creates a TargetGroup containing the specified Target.
func NewGroup(t *Target) *TargetGroup {
	if t.partOfGroup {
		panic("internal error: target is already part of a group")
	}
	t.partOfGroup = true
	if t.Breakpoints().Logical == nil {
		t.Breakpoints().Logical = make(map[int]*LogicalBreakpoint)
	}
	return &TargetGroup{
		RecordingManipulation: t.recman,
		targets:               []*Target{t},
		Selected:              t,
		cctx:                  &ContinueOnceContext{},
		recman:                t.recman,
		LogicalBreakpoints:    t.Breakpoints().Logical,
		continueOnce:          t.continueOnce,
	}
}

// Targets returns a slice of targets in the group.
func (grp *TargetGroup) Targets() []*Target {
	return grp.targets
}

// Valid returns true if any target in the target group is valid.
func (grp *TargetGroup) Valid() (bool, error) {
	var err0 error
	for _, t := range grp.targets {
		ok, err := t.Valid()
		if ok {
			return true, nil
		}
		err0 = err
	}
	return false, err0
}

// Detach detaches all targets in the group.
func (grp *TargetGroup) Detach(kill bool) error {
	var errs []string
	for _, t := range grp.targets {
		isvalid, _ := t.Valid()
		if !isvalid {
			continue
		}
		err := t.detach(kill)
		if err != nil {
			errs = append(errs, fmt.Sprintf("could not detach process %d: %v", t.Pid(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

// HasSteppingBreakpoints returns true if any of the targets has stepping breakpoints set.
func (grp *TargetGroup) HasSteppingBreakpoints() bool {
	for _, t := range grp.targets {
		if t.Breakpoints().HasSteppingBreakpoints() {
			return true
		}
	}
	return false
}

// ClearSteppingBreakpoints removes all stepping breakpoints.
func (grp *TargetGroup) ClearSteppingBreakpoints() error {
	for _, t := range grp.targets {
		if t.Breakpoints().HasSteppingBreakpoints() {
			return t.ClearSteppingBreakpoints()
		}
	}
	return nil
}

// ThreadList returns a list of all threads in all target processes.
func (grp *TargetGroup) ThreadList() []Thread {
	r := []Thread{}
	for _, t := range grp.targets {
		r = append(r, t.ThreadList()...)
	}
	return r
}

// TargetForThread returns the target containing the given thread.
func (grp *TargetGroup) TargetForThread(thread Thread) *Target {
	for _, t := range grp.targets {
		for _, th := range t.ThreadList() {
			if th == thread {
				return t
			}
		}
	}
	return nil
}
