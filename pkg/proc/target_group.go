package proc

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// TargetGroup reperesents a group of target processes being debugged that
// will be resumed and stopped simultaneously.
// New targets are automatically added to the group if exec catching is
// enabled and the backend supports it, otherwise the group will always
// contain a single target process.
type TargetGroup struct {
	targets []*Target
	procs   []ProcessInternal
	Sel     *Target

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
	return &TargetGroup{
		RecordingManipulation: t.recman,
		targets:               []*Target{t},
		procs:                 []ProcessInternal{t.proc},
		Sel:                   t,
		cctx:                  &ContinueOnceContext{},
		recman:                t.recman,
		LogicalBreakpoints:    t.Breakpoints().Logical,
		continueOnce:          t.continueOnce,
	}
}

// NewGroupWithBreakpoints creates a new group of targets containing t and
// sets the breakpoints in logicalBreakpoints.
// Breakpoints that can not be set will be discarded, if discard is not nil
// it will be called for each discarded breakpoint.
func NewGroupWithBreakpoints(t *Target, logicalBreakpoints map[int]*LogicalBreakpoint, discard func(*LogicalBreakpoint, error)) *TargetGroup {
	grp := NewGroup(t)
	grp.LogicalBreakpoints = logicalBreakpoints
	t.Breakpoints().Logical = logicalBreakpoints
	for _, bp := range grp.LogicalBreakpoints {
		if bp.LogicalID < 0 || !bp.Enabled {
			continue
		}
		if len(bp.File) > 0 {
			bp.TotalHitCount = 0
			bp.HitCount = make(map[int]uint64)
			err := grp.EnableBreakpoint(bp)
			if err != nil {
				if discard != nil {
					discard(bp, err)
				}
				delete(grp.LogicalBreakpoints, bp.LogicalID)
			}
		} else {
			if discard != nil {
				discard(bp, errors.New("can not recreate watchpoint on restart"))
			}
		}
	}
	return grp
}

// AllTargets returns a slice of all targets in the group, including the
// ones that are no longer valid.
func (grp *TargetGroup) AllTargets() []*Target {
	return grp.targets
}

// ValidTargets returns a slice of all targets in the group that are valid.
func (grp *TargetGroup) ValidTargets() []*Target {
	r := []*Target{}
	for _, t := range grp.targets {
		if isvalid, _ := t.Valid(); isvalid {
			r = append(r, t)
		}
	}
	return r
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

// GetTarget returns the target with the given PID.
func (grp *TargetGroup) GetTarget(pid int) *Target {
	for _, t := range grp.targets {
		if t.Pid() == pid {
			return t
		}
	}
	return nil
}

// EnableBreakpoint re-enables a disabled logical breakpoint.
func (grp *TargetGroup) EnableBreakpoint(lbp *LogicalBreakpoint) error {
	var err0 error
	bps := []*Breakpoint{}
	bpps := []*Target{}
targetLoop:
	for _, p := range grp.ValidTargets() {
		addrs, err := FindFileLocation(p, lbp.File, lbp.Line)
		if err != nil {
			if errNotFound, _ := err.(*ErrCouldNotFindLine); errNotFound != nil {
				continue
			}
			err0 = err
			break
		}
		for _, addr := range addrs {
			bp, err := p.SetBreakpoint(lbp.LogicalID, addr, UserBreakpoint, nil)
			if err != nil {
				if _, isexists := err.(BreakpointExistsError); isexists {
					continue
				}
				err0 = err
				break targetLoop
			}
			bps = append(bps, bp)
			bpps = append(bpps, p)
		}
	}
	if err0 != nil {
		for i, bp := range bps {
			if bp == nil {
				continue
			}
			p := bpps[i]
			if err1 := p.ClearBreakpoint(bp.Addr); err1 != nil {
				return fmt.Errorf("error while creating breakpoint: %v, additionally the breakpoint could not be properly rolled back: %v", err0, err1)
			}
		}
		return err0
	}
	lbp.Enabled = true
	return nil
}

// DisableBreakpoint disables a logical breakpoint.
//
// TODO(aarzilli): it's possible to create breakpoints on arbitrary
// addresses, that either are not associated with any file:line pair or
// that are associated with only some of the addresses associated with a
// file:line pair. Disabling and re-enabling a breakpoint will eliminate
// this distinction. This is a problem without a simple solution, since
// disabled breakpoints can survive target restarts.
// A similar issue is present with watchpoints.
func (grp *TargetGroup) DisableBreakpoint(lbp *LogicalBreakpoint) error {
	var errs []error
	n := 0
	for _, p := range grp.ValidTargets() {
		for _, bp := range p.Breakpoints().M {
			if bp.LogicalID() == lbp.LogicalID {
				n++
				err := p.ClearBreakpoint(bp.Addr)
				if err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	if len(errs) > 0 {
		buf := new(bytes.Buffer)
		for i, err := range errs {
			fmt.Fprintf(buf, "%s", err)
			if i != len(errs)-1 {
				fmt.Fprintf(buf, ", ")
			}
		}

		if len(errs) == n {
			return fmt.Errorf("unable to clear breakpoint %d: %v", lbp.LogicalID, buf.String())
		}
		return fmt.Errorf("unable to clear breakpoint %d (partial): %s", lbp.LogicalID, buf.String())
	}
	lbp.Enabled = false
	return nil
}
