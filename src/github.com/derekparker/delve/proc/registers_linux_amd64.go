package proc

import "fmt"
import "bytes"
import sys "golang.org/x/sys/unix"

type Regs struct {
	regs *sys.PtraceRegs
}

func (r *Regs) String() string {
	var buf bytes.Buffer
	var regs = []struct {
		k string
		v uint64
	}{
		{"Rip", r.regs.Rip},
		{"Rsp", r.regs.Rsp},
		{"Rax", r.regs.Rax},
		{"Rbx", r.regs.Rbx},
		{"Rcx", r.regs.Rcx},
		{"Rdx", r.regs.Rdx},
		{"Rdi", r.regs.Rdi},
		{"Rsi", r.regs.Rsi},
		{"Rbp", r.regs.Rbp},
		{"R8", r.regs.R8},
		{"R9", r.regs.R9},
		{"R10", r.regs.R10},
		{"R11", r.regs.R11},
		{"R12", r.regs.R12},
		{"R13", r.regs.R13},
		{"R14", r.regs.R14},
		{"R15", r.regs.R15},
		{"Orig_rax", r.regs.Orig_rax},
		{"Cs", r.regs.Cs},
		{"Eflags", r.regs.Eflags},
		{"Ss", r.regs.Ss},
		{"Fs_base", r.regs.Fs_base},
		{"Gs_base", r.regs.Gs_base},
		{"Ds", r.regs.Ds},
		{"Es", r.regs.Es},
		{"Fs", r.regs.Fs},
		{"Gs", r.regs.Gs},
	}
	for _, reg := range regs {
		fmt.Fprintf(&buf, "%8s = %0#16x\n", reg.k, reg.v)
	}
	return buf.String()
}

func (r *Regs) PC() uint64 {
	return r.regs.PC()
}

func (r *Regs) SP() uint64 {
	return r.regs.Rsp
}

func (r *Regs) CX() uint64 {
	return r.regs.Rcx
}

func (r *Regs) TLS() uint64 {
	return r.regs.Fs_base
}

func (r *Regs) SetPC(thread *Thread, pc uint64) (err error) {
	r.regs.SetPC(pc)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.Id, r.regs) })
	return
}

func registers(thread *Thread) (Registers, error) {
	var (
		regs sys.PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(thread.Id, &regs) })
	if err != nil {
		return nil, err
	}
	return &Regs{&regs}, nil
}
