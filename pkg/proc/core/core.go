package core

import (
	"errors"
	"fmt"
	"go/ast"
	"io"
	"sync"

	"github.com/derekparker/delve/pkg/proc"
)

// A SplicedMemory represents a memory space formed from multiple regions,
// each of which may override previously regions. For example, in the following
// core, the program text was loaded at 0x400000:
// Start               End                 Page Offset
// 0x0000000000400000  0x000000000044f000  0x0000000000000000
// but then it's partially overwritten with an RW mapping whose data is stored
// in the core file:
// Type           Offset             VirtAddr           PhysAddr
//                FileSiz            MemSiz              Flags  Align
// LOAD           0x0000000000004000 0x000000000049a000 0x0000000000000000
//                0x0000000000002000 0x0000000000002000  RW     1000
// This can be represented in a SplicedMemory by adding the original region,
// then putting the RW mapping on top of it.
type SplicedMemory struct {
	readers []readerEntry
}

type readerEntry struct {
	offset uintptr
	length uintptr
	reader proc.MemoryReader
}

// Add adds a new region to the SplicedMemory, which may override existing regions.
func (r *SplicedMemory) Add(reader proc.MemoryReader, off, length uintptr) {
	if length == 0 {
		return
	}
	end := off + length - 1
	newReaders := make([]readerEntry, 0, len(r.readers))
	add := func(e readerEntry) {
		if e.length == 0 {
			return
		}
		newReaders = append(newReaders, e)
	}
	inserted := false
	// Walk through the list of regions, fixing up any that overlap and inserting the new one.
	for _, entry := range r.readers {
		entryEnd := entry.offset + entry.length - 1
		switch {
		case entryEnd < off:
			// Entry is completely before the new region.
			add(entry)
		case end < entry.offset:
			// Entry is completely after the new region.
			if !inserted {
				add(readerEntry{off, length, reader})
				inserted = true
			}
			add(entry)
		case off <= entry.offset && entryEnd <= end:
			// Entry is completely overwritten by the new region. Drop.
		case entry.offset < off && entryEnd <= end:
			// New region overwrites the end of the entry.
			entry.length = off - entry.offset
			add(entry)
		case off <= entry.offset && end < entryEnd:
			// New reader overwrites the beginning of the entry.
			if !inserted {
				add(readerEntry{off, length, reader})
				inserted = true
			}
			overlap := entry.offset - off
			entry.offset += overlap
			entry.length -= overlap
			add(entry)
		case entry.offset < off && end < entryEnd:
			// New region punches a hole in the entry. Split it in two and put the new region in the middle.
			add(readerEntry{entry.offset, off - entry.offset, entry.reader})
			add(readerEntry{off, length, reader})
			add(readerEntry{end + 1, entryEnd - end, entry.reader})
			inserted = true
		default:
			panic(fmt.Sprintf("Unhandled case: existing entry is %v len %v, new is %v len %v", entry.offset, entry.length, off, length))
		}
	}
	if !inserted {
		newReaders = append(newReaders, readerEntry{off, length, reader})
	}
	r.readers = newReaders
}

// ReadMemory implements MemoryReader.ReadMemory.
func (r *SplicedMemory) ReadMemory(buf []byte, addr uintptr) (n int, err error) {
	started := false
	for _, entry := range r.readers {
		if entry.offset+entry.length < addr {
			if !started {
				continue
			}
			return n, fmt.Errorf("hit unmapped area at %v after %v bytes", addr, n)
		}

		// Don't go past the region.
		pb := buf
		if addr+uintptr(len(buf)) > entry.offset+entry.length {
			pb = pb[:entry.offset+entry.length-addr]
		}
		pn, err := entry.reader.ReadMemory(pb, addr)
		n += pn
		if err != nil || pn != len(pb) {
			return n, err
		}
		buf = buf[pn:]
		addr += uintptr(pn)
		if len(buf) == 0 {
			// Done, don't bother scanning the rest.
			return n, nil
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("offset %v did not match any regions", addr)
	}
	return n, nil
}

// OffsetReaderAt wraps a ReaderAt into a MemoryReader, subtracting a fixed
// offset from the address. This is useful to represent a mapping in an address
// space. For example, if program text is mapped in at 0x400000, an
// OffsetReaderAt with offset 0x400000 can be wrapped around file.Open(program)
// to return the results of a read in that part of the address space.
type OffsetReaderAt struct {
	reader io.ReaderAt
	offset uintptr
}

func (r *OffsetReaderAt) ReadMemory(buf []byte, addr uintptr) (n int, err error) {
	return r.reader.ReadAt(buf, int64(addr-r.offset))
}

type Process struct {
	bi                proc.BinaryInfo
	core              *Core
	breakpoints       map[uint64]*proc.Breakpoint
	currentThread     *Thread
	selectedGoroutine *proc.G
	allGCache         []*proc.G
}

type Thread struct {
	th     *LinuxPrStatus
	fpregs []proc.Register
	p      *Process
}

var ErrWriteCore = errors.New("can not to core process")
var ErrShortRead = errors.New("short read")
var ErrContinueCore = errors.New("can not continue execution of core process")

func OpenCore(corePath, exePath string) (*Process, error) {
	core, err := readCore(corePath, exePath)
	if err != nil {
		return nil, err
	}
	p := &Process{
		core:        core,
		breakpoints: make(map[uint64]*proc.Breakpoint),
		bi:          proc.NewBinaryInfo("linux", "amd64"),
	}
	for _, thread := range core.Threads {
		thread.p = p
	}

	var wg sync.WaitGroup
	err = p.bi.LoadBinaryInfo(exePath, &wg)
	wg.Wait()
	if err == nil {
		err = p.bi.LoadError()
	}
	if err != nil {
		return nil, err
	}

	for _, th := range p.core.Threads {
		p.currentThread = th
		break
	}
	p.selectedGoroutine, _ = proc.GetG(p.CurrentThread())

	return p, nil
}

func (p *Process) BinInfo() *proc.BinaryInfo {
	return &p.bi
}

func (p *Process) Recorded() (bool, string)                { return true, "" }
func (p *Process) Restart(string) error                    { return ErrContinueCore }
func (p *Process) Direction(proc.Direction) error          { return ErrContinueCore }
func (p *Process) When() (string, error)                   { return "", nil }
func (p *Process) Checkpoint(string) (int, error)          { return -1, ErrContinueCore }
func (p *Process) Checkpoints() ([]proc.Checkpoint, error) { return nil, nil }
func (p *Process) ClearCheckpoint(int) error               { return errors.New("checkpoint not found") }

func (thread *Thread) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	n, err = thread.p.core.ReadMemory(data, addr)
	if err == nil && n != len(data) {
		err = ErrShortRead
	}
	return n, err
}

func (thread *Thread) WriteMemory(addr uintptr, data []byte) (int, error) {
	return 0, ErrWriteCore
}

func (t *Thread) Location() (*proc.Location, error) {
	f, l, fn := t.p.bi.PCToLine(t.th.Reg.Rip)
	return &proc.Location{PC: t.th.Reg.Rip, File: f, Line: l, Fn: fn}, nil
}

func (t *Thread) Breakpoint() (*proc.Breakpoint, bool, error) {
	return nil, false, nil
}

func (t *Thread) ThreadID() int {
	return int(t.th.Pid)
}

func (t *Thread) Registers(floatingPoint bool) (proc.Registers, error) {
	r := &Registers{&t.th.Reg, nil}
	if floatingPoint {
		r.fpregs = t.fpregs
	}
	return r, nil
}

func (t *Thread) Arch() proc.Arch {
	return t.p.bi.Arch
}

func (t *Thread) BinInfo() *proc.BinaryInfo {
	return &t.p.bi
}

func (t *Thread) StepInstruction() error {
	return ErrContinueCore
}

func (t *Thread) Blocked() bool {
	return false
}

func (t *Thread) SetCurrentBreakpoint() error {
	return nil
}

func (p *Process) Breakpoints() map[uint64]*proc.Breakpoint {
	return p.breakpoints
}

func (p *Process) ClearBreakpoint(addr uint64) (*proc.Breakpoint, error) {
	return nil, proc.NoBreakpointError{Addr: addr}
}

func (p *Process) ClearInternalBreakpoints() error {
	return nil
}

func (p *Process) ContinueOnce() (proc.Thread, error) {
	return nil, ErrContinueCore
}

func (p *Process) StepInstruction() error {
	return ErrContinueCore
}

func (p *Process) RequestManualStop() error {
	return nil
}

func (p *Process) ManualStopRequested() bool {
	return false
}

func (p *Process) CurrentThread() proc.Thread {
	return p.currentThread
}

func (p *Process) Detach(bool) error {
	return nil
}

func (p *Process) Exited() bool {
	return false
}

func (p *Process) AllGCache() *[]*proc.G {
	return &p.allGCache
}

func (p *Process) Halt() error {
	return nil
}

func (p *Process) Kill() error {
	return nil
}

func (p *Process) Pid() int {
	return p.core.Pid
}

func (p *Process) ResumeNotify(chan<- struct{}) {
}

func (p *Process) SelectedGoroutine() *proc.G {
	return p.selectedGoroutine
}

func (p *Process) SetBreakpoint(addr uint64, kind proc.BreakpointKind, cond ast.Expr) (*proc.Breakpoint, error) {
	return nil, ErrWriteCore
}

func (p *Process) SwitchGoroutine(gid int) error {
	g, err := proc.FindGoroutine(p, gid)
	if err != nil {
		return err
	}
	if g == nil {
		// user specified -1 and selectedGoroutine is nil
		return nil
	}
	if g.Thread != nil {
		return p.SwitchThread(g.Thread.ThreadID())
	}
	p.selectedGoroutine = g
	return nil
}

func (p *Process) SwitchThread(tid int) error {
	if th, ok := p.core.Threads[tid]; ok {
		p.currentThread = th
		p.selectedGoroutine, _ = proc.GetG(p.CurrentThread())
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}

func (p *Process) ThreadList() []proc.Thread {
	r := make([]proc.Thread, 0, len(p.core.Threads))
	for _, v := range p.core.Threads {
		r = append(r, v)
	}
	return r
}

func (p *Process) FindThread(threadID int) (proc.Thread, bool) {
	t, ok := p.core.Threads[threadID]
	return t, ok
}

type Registers struct {
	*LinuxCoreRegisters
	fpregs []proc.Register
}

func (r *Registers) Slice() []proc.Register {
	var regs = []struct {
		k string
		v uint64
	}{
		{"Rip", r.Rip},
		{"Rsp", r.Rsp},
		{"Rax", r.Rax},
		{"Rbx", r.Rbx},
		{"Rcx", r.Rcx},
		{"Rdx", r.Rdx},
		{"Rdi", r.Rdi},
		{"Rsi", r.Rsi},
		{"Rbp", r.Rbp},
		{"R8", r.R8},
		{"R9", r.R9},
		{"R10", r.R10},
		{"R11", r.R11},
		{"R12", r.R12},
		{"R13", r.R13},
		{"R14", r.R14},
		{"R15", r.R15},
		{"Orig_rax", r.Orig_rax},
		{"Cs", r.Cs},
		{"Eflags", r.Eflags},
		{"Ss", r.Ss},
		{"Fs_base", r.Fs_base},
		{"Gs_base", r.Gs_base},
		{"Ds", r.Ds},
		{"Es", r.Es},
		{"Fs", r.Fs},
		{"Gs", r.Gs},
	}
	out := make([]proc.Register, 0, len(regs))
	for _, reg := range regs {
		if reg.k == "Eflags" {
			out = proc.AppendEflagReg(out, reg.k, reg.v)
		} else {
			out = proc.AppendQwordReg(out, reg.k, reg.v)
		}
	}
	out = append(out, r.fpregs...)
	return out
}
