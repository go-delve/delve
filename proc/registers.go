package proc

import "fmt"

// An interface for a generic register type. The
// interface encapsulates the generic values / actions
// we need independant of arch. The concrete register types
// will be different depending on OS/Arch.
type Registers interface {
	PC() uint64
	SP() uint64
	CX() uint64
	SetPC(*Thread, uint64) error
}

// Obtains register values from the debugged process.
func (thread *Thread) Registers() (Registers, error) {
	regs, err := registers(thread)
	if err != nil {
		return nil, fmt.Errorf("could not get registers: %s", err)
	}
	return regs, nil
}

// Returns the current PC for this thread.
func (thread *Thread) PC() (uint64, error) {
	regs, err := thread.Registers()
	if err != nil {
		return 0, err
	}
	return regs.PC(), nil
}
