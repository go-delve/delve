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

// Restart will start the process over from the location specified by the "from" locspec.
// This is only useful for recorded targets.
// Restarting of a normal process happens at a higher level (debugger.Restart).
func (t *Target) Restart(from string) error {
	t.ClearAllGCache()
	return t.Process.Restart(from)
}

// Detach will detach the target from the underylying process.
// This means the debugger will no longer receive events from the process
// we were previously debugging.
// If kill is true then the process will be killed when we detach.
func (t *Target) Detach(kill bool) error {
	if !kill && t.asyncPreemptChanged {
		setAsyncPreemptOff(t, t.asyncPreemptOff)
	}
	return t.Process.Detach(kill)
}
