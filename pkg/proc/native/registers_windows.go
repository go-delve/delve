package native

import "github.com/go-delve/delve/pkg/dwarf/op"

// SetPC sets the RIP register to the value specified by `pc`.
func (thread *nativeThread) setPC(pc uint64) error {
	context := newContext()
	context.SetFlags(_CONTEXT_ALL)

	err := thread.getContext(context)
	if err != nil {
		return err
	}

	context.SetPC(pc)

	return thread.setContext(context)
}

// SetReg changes the value of the specified register.
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	context := newContext()
	context.SetFlags(_CONTEXT_ALL)
	err := thread.getContext(context)
	if err != nil {
		return err
	}

	err = context.SetReg(regNum, reg)
	if err != nil {
		return err
	}

	return thread.setContext(context)
}
