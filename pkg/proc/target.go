package proc

import (
	"fmt"
)

// Target represents the process being debugged.
type Target struct {
	Process

	// Goroutine that will be used by default to set breakpoint, eval variables, etc...
	// Normally selectedGoroutine is currentThread.GetG, it will not be only if SwitchGoroutine is called with a goroutine that isn't attached to a thread
	selectedGoroutine *G

	// fncallForG stores a mapping of current active function calls.
	fncallForG map[int]*callInjection

	// gcache is a cache for Goroutines that we
	// have read and parsed from the targets memory.
	// This must be cleared whenever the target is resumed.
	gcache goroutineCache
}

// NewTarget returns an initialized Target object.
func NewTarget(p Process, path string, debugInfoDirs []string, writeBreakpoint WriteBreakpointFn) (*Target, error) {
	entryPoint, err := p.EntryPoint()
	if err != nil {
		return nil, err
	}

	err = p.BinInfo().LoadBinaryInfo(path, entryPoint, debugInfoDirs)
	if err != nil {
		return nil, err
	}
	for _, image := range p.BinInfo().Images {
		if image.loadErr != nil {
			return nil, image.loadErr
		}
	}

	t := &Target{
		Process:    p,
		fncallForG: make(map[int]*callInjection),
	}

	g, _ := GetG(p.CurrentThread())
	t.selectedGoroutine = g

	createUnrecoveredPanicBreakpoint(p, writeBreakpoint)
	createFatalThrowBreakpoint(p, writeBreakpoint)

	t.gcache.init(p.BinInfo())
	return t, nil
}

// SupportsFunctionCalls returns whether or not the backend supports
// calling functions during a debug session.
// Currently only non-recorded processes running on AMD64 support
// function calls.
func (t *Target) SupportsFunctionCalls() bool {
	if ok, _ := t.Process.Recorded(); ok {
		return false
	}
	_, ok := t.Process.BinInfo().Arch.(*AMD64)
	return ok
}

// ClearAllGCache clears the internal Goroutine cache.
// This should be called anytime the target process executes instructions.
func (t *Target) ClearAllGCache() {
	t.gcache.Clear()
	for _, thread := range t.ThreadList() {
		thread.Common().g = nil
	}
}

func (t *Target) Restart(from string) error {
	t.ClearAllGCache()
	err := t.Process.Restart(from)
	if err != nil {
		return err
	}
	t.selectedGoroutine, _ = GetG(t.CurrentThread())
	return nil
}

// SetSelectedGoroutine will set internally the goroutine that should be
// the default for any command executed, the goroutine being actively
// followed.
func (t *Target) SelectedGoroutine() *G {
	return t.selectedGoroutine
}

// SwitchGoroutine will change the selected and active goroutine.
func (p *Target) SwitchGoroutine(g *G) error {
	if ok, err := p.Valid(); !ok {
		return err
	}
	if g == nil {
		return nil
	}
	if g.Thread != nil {
		return p.SwitchThread(g.Thread.ThreadID())
	}
	p.selectedGoroutine = g
	return nil
}

// SwitchThread will change the selected and active thread.
func (p *Target) SwitchThread(tid int) error {
	if ok, err := p.Valid(); !ok {
		return err
	}
	if th, ok := p.FindThread(tid); ok {
		p.InternalSetCurrentThread(th)
		p.selectedGoroutine, _ = GetG(p.CurrentThread())
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}
