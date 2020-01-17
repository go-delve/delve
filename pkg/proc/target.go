package proc

type Target struct {
	Process
}

func NewTarget(p Process) *Target {
	return &Target{Process: p}
}
