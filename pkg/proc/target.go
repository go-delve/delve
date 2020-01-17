package proc

// Target represents the process being debugged.
type Target struct {
	Process

	// fncallForG stores a mapping of current active function calls.
	fncallForG map[int]*callInjection
}

// NewTarget returns an initialized Target object.
func NewTarget(p Process) *Target {
	return &Target{
		Process:    p,
		fncallForG: make(map[int]*callInjection),
	}
}

func (t *Target) ClearAllGCache() {
	t.Process.Common().ClearAllGCache()
}
