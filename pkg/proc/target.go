package proc

// Target represents the process being debugged.
type Target struct {
	Process

	// fncallForG stores a mapping of current active function calls.
	fncallForG map[int]*callInjection

	// gcache is a cache for Goroutines that we
	// have read and parsed from the targets memory.
	// This must be cleared whenever the target is resumed.
	gcache goroutineCache
}

// NewTarget returns an initialized Target object.
func NewTarget(p Process) *Target {
	t := &Target{
		Process:    p,
		fncallForG: make(map[int]*callInjection),
	}
	t.gcache.init(p.BinInfo())
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

func (t *Target) Restart(from string) error {
	t.ClearAllGCache()
	return t.Process.Restart(from)
}
