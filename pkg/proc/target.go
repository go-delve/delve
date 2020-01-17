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

func (t *Target) ClearAllGCache() {
	t.gcache.Clear()
}
