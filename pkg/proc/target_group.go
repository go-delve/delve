package proc

import (
	"bytes"
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

// NewGroupRestart creates a new group of targets containing t and
// sets breakpoints and other attributes from oldgrp.
// Breakpoints that can not be set will be discarded, if discard is not nil
// it will be called for each discarded breakpoint.
func NewGroupRestart(t *Target, oldgrp *TargetGroup, discard func(*LogicalBreakpoint, error)) *TargetGroup {
	grp := NewGroup(t)
	grp.LogicalBreakpoints = oldgrp.LogicalBreakpoints
	t.Breakpoints().Logical = grp.LogicalBreakpoints
	for _, bp := range grp.LogicalBreakpoints {
		if bp.LogicalID < 0 || !bp.Enabled {
			continue
		}
		bp.TotalHitCount = 0
		bp.HitCount = make(map[int64]uint64)
		bp.Set.PidAddrs = nil // breakpoints set through a list of addresses can not be restored after a restart
		err := grp.EnableBreakpoint(bp)
		if err != nil {
			if discard != nil {
				discard(bp, err)
			}
			delete(grp.LogicalBreakpoints, bp.LogicalID)
		}
	}
	return grp
}

// Targets returns a slice of all targets in the group, including the
// ones that are no longer valid.
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
		if err0 == nil {
			err0 = err
		}
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

// EnableBreakpoint re-enables a disabled logical breakpoint.
func (grp *TargetGroup) EnableBreakpoint(lbp *LogicalBreakpoint) error {
	var err0, errNotFound, errExists error
	didSet := false
targetLoop:
	for _, p := range grp.targets {
		err := enableBreakpointOnTarget(p, lbp)

		switch err.(type) {
		case nil:
			didSet = true
		case *ErrFunctionNotFound, *ErrCouldNotFindLine:
			errNotFound = err
		case BreakpointExistsError:
			errExists = err
		default:
			err0 = err
			break targetLoop
		}
	}
	if errNotFound != nil && !didSet {
		return errNotFound
	}
	if errExists != nil && !didSet {
		return errExists
	}
	if !didSet {
		if _, err := grp.Valid(); err != nil {
			return err
		}
	}
	if err0 != nil {
		it := ValidTargets{Group: grp}
		for it.Next() {
			for _, bp := range it.Breakpoints().M {
				if bp.LogicalID() == lbp.LogicalID {
					if err1 := it.ClearBreakpoint(bp.Addr); err1 != nil {
						return fmt.Errorf("error while creating breakpoint: %v, additionally the breakpoint could not be properly rolled back: %v", err0, err1)
					}
				}
			}
		}
		return err0
	}
	lbp.Enabled = true
	return nil
}

func enableBreakpointOnTarget(p *Target, lbp *LogicalBreakpoint) error {
	var err error
	var addrs []uint64
	switch {
	case lbp.Set.File != "":
		addrs, err = FindFileLocation(p, lbp.Set.File, lbp.Set.Line)
	case lbp.Set.FunctionName != "":
		addrs, err = FindFunctionLocation(p, lbp.Set.FunctionName, lbp.Set.Line)
	case len(lbp.Set.PidAddrs) > 0:
		for _, pidAddr := range lbp.Set.PidAddrs {
			if pidAddr.Pid == p.Pid() {
				addrs = append(addrs, pidAddr.Addr)
			}
		}
	case lbp.Set.Expr != nil:
		addrs = lbp.Set.Expr(p)
	default:
		return fmt.Errorf("breakpoint %d can not be enabled", lbp.LogicalID)
	}

	if err != nil {
		return err
	}

	for _, addr := range addrs {
		_, err = p.SetBreakpoint(lbp.LogicalID, addr, UserBreakpoint, nil)
		if err != nil {
			if _, isexists := err.(BreakpointExistsError); isexists {
				continue
			}
			return err
		}
	}

	return err
}

// DisableBreakpoint disables a logical breakpoint.
func (grp *TargetGroup) DisableBreakpoint(lbp *LogicalBreakpoint) error {
	var errs []error
	n := 0
	it := ValidTargets{Group: grp}
	for it.Next() {
		for _, bp := range it.Breakpoints().M {
			if bp.LogicalID() == lbp.LogicalID {
				n++
				err := it.ClearBreakpoint(bp.Addr)
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

// ValidTargets iterates through all valid targets in Group.
type ValidTargets struct {
	*Target
	Group *TargetGroup
	start int
}

// Next moves to the next valid target, returns false if there aren't more
// valid targets in the group.
func (it *ValidTargets) Next() bool {
	for i := it.start; i < len(it.Group.targets); i++ {
		if ok, _ := it.Group.targets[i].Valid(); ok {
			it.Target = it.Group.targets[i]
			it.start = i + 1
			return true
		}
	}
	it.start = len(it.Group.targets)
	it.Target = nil
	return false
}
