package proc

// Thread represents a thread.
type Thread interface {
	MemoryReadWriter
	ThreadID() int

	StepInstruction() error
	// Blocked returns true if the thread is blocked
	Blocked() bool
	// Common returns the CommonThread structure for this thread
	Common() *CommonThread

	// Registers returns the CPU registers of this thread. The contents of the
	// variable returned may or may not change to reflect the new CPU status
	// when the thread is resumed or the registers are changed by calling
	// SetPC/SetSP/etc.
	// To insure that the the returned variable won't change call the Copy
	// method of Registers.
	Registers(floatingPoint bool) (Registers, error)

	// RestoreRegisters restores saved registers
	RestoreRegisters(Registers) error
	PC() (uint64, error)
	SetPC(uint64) error
	SetSP(uint64) error
	SetDX(uint64) error
}

// ErrThreadBlocked is returned when the thread
// is blocked in the scheduler.
type ErrThreadBlocked struct{}

func (tbe ErrThreadBlocked) Error() string {
	return "thread blocked"
}

// CommonThread contains fields used by this package, common to all
// implementations of the Thread interface.
type CommonThread struct {
}
