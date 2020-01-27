package proc

// Target represents the process being debugged.
type Target struct {
	Process

	// fncallForG stores a mapping of current active function calls.
	fncallForG map[int]*callInjection

	asyncPreemptChanged bool  // runtime/debug.asyncpreemptoff was changed
	asyncPreemptOff     int64 // cached value of runtime/debug.asyncpreemptoff

	// gcache is a cache for Goroutines that we
	// have read and parsed from the targets memory.
	// This must be cleared whenever the target is resumed.
	gcache goroutineCache
}

// NewTarget returns an initialized Target object.
func NewTarget(p Process, disableAsyncPreempt bool) *Target {
	t := &Target{
		Process:    p,
		fncallForG: make(map[int]*callInjection),
	}
	t.gcache.init(p.BinInfo())

	if disableAsyncPreempt {
		setAsyncPreemptOff(t, 1)
	}

	return t
}

// ClearBreakpoint clears the breakpoint at addr.
func (t *Target) ClearBreakpoint(addr uint64) (*Breakpoint, error) {
	if ok, err := t.Valid(); !ok {
		return nil, err
	}
	bp, err := t.Process.ClearBreakpoint(addr)
	if err != nil {
		return nil, err
	}
	for _, th := range t.Process.ThreadList() {
		bp := th.Breakpoint()
		if bp.Breakpoint != nil && bp.Addr == addr {
			bp.Clear()
		}
	}
	return bp, nil
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
}

// Restart starts the process over from the given location.
// Only valid for recorded targets.
func (t *Target) Restart(from string) error {
	t.ClearAllGCache()
	return t.Process.Restart(from)
}

func (t *Target) Detach(kill bool) error {
	if !kill && t.asyncPreemptChanged {
		setAsyncPreemptOff(t, t.asyncPreemptOff)
	}
	return t.Process.Detach(kill)
}
