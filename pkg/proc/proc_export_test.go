package proc

// Wrapper functions so that most tests proc_test.go don't need to worry
// about TargetGroup when they just need to resume a single process.

func newGroupTransient(tgt *Target) *TargetGroup {
	grp := NewGroup(tgt)
	tgt.partOfGroup = false
	return grp
}

func (tgt *Target) Detach(kill bool) error {
	return tgt.detach(kill)
}

func (tgt *Target) Continue() error {
	return newGroupTransient(tgt).Continue()
}

func (tgt *Target) Next() error {
	return newGroupTransient(tgt).Next()
}

func (tgt *Target) Step() error {
	return newGroupTransient(tgt).Step()
}

func (tgt *Target) StepOut() error {
	return newGroupTransient(tgt).StepOut()
}

func (tgt *Target) ChangeDirection(dir Direction) error {
	return tgt.recman.ChangeDirection(dir)
}

func (tgt *Target) StepInstruction() error {
	return newGroupTransient(tgt).StepInstruction()
}

func (tgt *Target) Recorded() (bool, string) {
	return tgt.recman.Recorded()
}
