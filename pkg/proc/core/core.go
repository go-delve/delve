package core

import (
	"errors"
	"fmt"
	"io"

	"github.com/go-delve/delve/pkg/proc"
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

		// The reading of the memory has been started after the first iteration
		started = true

		// Don't go past the region.
		pb := buf
		if addr+uintptr(len(buf)) > entry.offset+entry.length {
			pb = pb[:entry.offset+entry.length-addr]
		}
		pn, err := entry.reader.ReadMemory(pb, addr)
		n += pn
		if err != nil {
			return n, fmt.Errorf("error while reading spliced memory at %#x: %v", addr, err)
		}
		if pn != len(pb) {
			return n, nil
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

// ReadMemory will read the memory at addr-offset.
func (r *OffsetReaderAt) ReadMemory(buf []byte, addr uintptr) (n int, err error) {
	return r.reader.ReadAt(buf, int64(addr-r.offset))
}

// Process represents a core file.
type Process struct {
	// TODO(refactor) REMOVE BEFORE MERGE
	t proc.Process

	mem     proc.MemoryReader
	Threads map[int]*Thread
	pid     int

	entryPoint uint64

	common proc.CommonProcess
}

// Thread represents a thread in the core file being debugged.
type Thread struct {
	th     osThread
	p      *Process
	common proc.CommonThread
}

type osThread interface {
	registers(floatingPoint bool) (proc.Registers, error)
	pid() int
}

var (
	// ErrWriteCore is returned when attempting to write to the core
	// process memory.
	ErrWriteCore = errors.New("can not write to core process")

	// ErrShortRead is returned on a short read.
	ErrShortRead = errors.New("short read")

	// ErrContinueCore is returned when trying to continue execution of a core process.
	ErrContinueCore = errors.New("can not continue execution of core process")

	// ErrChangeRegisterCore is returned when trying to change register values for core files.
	ErrChangeRegisterCore = errors.New("can not change register values of core process")
)

type openFn func(string, string) (*Process, string, string, error)

var openFns = []openFn{readLinuxAMD64Core, readAMD64Minidump}

// ErrUnrecognizedFormat is returned when the core file is not recognized as
// any of the supported formats.
var ErrUnrecognizedFormat = errors.New("unrecognized core format")

// OpenCore will open the core file and return a Process struct.
// If the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func OpenCore(corePath, exePath string) (*Process, string, string, error) {
	var p *Process
	var err error
	var os, arch string
	for _, openFn := range openFns {
		p, os, arch, err = openFn(corePath, exePath)
		if err != ErrUnrecognizedFormat {
			break
		}
	}
	if err != nil {
		return nil, "", "", err
	}
	p.Common().ExePath = exePath

	return p, os, arch, nil
}

func (p *Process) Initialize() error {
	return nil
}

func (p *Process) ExecutablePath() string {
	return p.Common().ExePath
}

func (p *Process) SetTarget(pp proc.Process) {
	p.t = pp
}

// EntryPoint will return the entry point address for this core file.
func (p *Process) EntryPoint() (uint64, error) {
	return p.entryPoint, nil
}

// WriteBreakpoint is a noop function since you
// cannot write breakpoints into core files.
func (p *Process) WriteBreakpoint(addr uint64, _ []byte) (originalData []byte, err error) {
	return nil, errors.New("cannot write a breakpoint to a core file")
}

// Recorded returns whether this is a live or recorded process. Always returns true for core files.
func (p *Process) Recorded() (bool, string) { return true, "" }

// Restart will only return an error for core files, as they are not executing.
func (p *Process) Restart(string) error { return ErrContinueCore }

// Direction will only return an error as you cannot continue a core process.
func (p *Process) ChangeDirection(proc.Direction) error { return ErrContinueCore }

func (p *Process) Direction() proc.Direction { return proc.Forward }

// When does not apply to core files, it is to support the Mozilla 'rr' backend.
func (p *Process) When() (string, error) { return "", nil }

// Checkpoint for core files returns an error, there is no execution of a core file.
func (p *Process) Checkpoint(string) (int, error) { return -1, ErrContinueCore }

// Checkpoints returns nil on core files, you cannot set checkpoints when debugging core files.
func (p *Process) Checkpoints() ([]proc.Checkpoint, error) { return nil, nil }

// ClearCheckpoint clears a checkpoint, but will only return an error for core files.
func (p *Process) ClearCheckpoint(int) error { return errors.New("checkpoint not found") }

func (p *Process) StepInstructionOut(proc.Thread, string, string) error {
	return ErrContinueCore
}

// ReadMemory will return memory from the core file at the specified location and put the
// read memory into `data`, returning the length read, and returning an error if
// the length read is shorter than the length of the `data` buffer.
func (t *Thread) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	n, err = t.p.mem.ReadMemory(data, addr)
	if err == nil && n != len(data) {
		err = ErrShortRead
	}
	return n, err
}

// WriteMemory will only return an error for core files, you cannot write
// to the memory of a core process.
func (t *Thread) WriteMemory(addr uintptr, data []byte) (int, error) {
	return 0, ErrWriteCore
}

// ThreadID returns the ID for this thread.
func (t *Thread) ThreadID() int {
	return int(t.th.pid())
}

func (t *Thread) PC() (uint64, error) {
	regs, err := t.Registers(false)
	if err != nil {
		return 0, err
	}
	return regs.PC(), nil
}

// Registers returns the current value of the registers for this thread.
func (t *Thread) Registers(floatingPoint bool) (proc.Registers, error) {
	return t.th.registers(floatingPoint)
}

// RestoreRegisters will only return an error for core files,
// you cannot change register values for core files.
func (t *Thread) RestoreRegisters(proc.Registers) error {
	return ErrChangeRegisterCore
}

// StepInstruction will only return an error for core files,
// you cannot execute a core file.
func (t *Thread) StepInstruction() error {
	return ErrContinueCore
}

// Blocked will return false always for core files as there is
// no execution.
func (t *Thread) Blocked() bool {
	return false
}

// SetCurrentBreakpoint will always just return nil
// for core files, as there are no breakpoints in core files.
func (t *Thread) SetCurrentBreakpoint(adjustPC bool) error {
	return nil
}

// Common returns a struct containing common information
// across thread implementations.
func (t *Thread) Common() *proc.CommonThread {
	return &t.common
}

// SetPC will always return an error, you cannot
// change register values when debugging core files.
func (t *Thread) SetPC(uint64) error {
	return ErrChangeRegisterCore
}

// SetSP will always return an error, you cannot
// change register values when debugging core files.
func (t *Thread) SetSP(uint64) error {
	return ErrChangeRegisterCore
}

// SetDX will always return an error, you cannot
// change register values when debugging core files.
func (t *Thread) SetDX(uint64) error {
	return ErrChangeRegisterCore
}

func (p *Process) ClearBreakpointFn(addr uint64, _ []byte) error {
	return errors.New("breakpoints are not applicable in core files")
}

// Resume will always return an error because you
// cannot control execution of a core file.
func (p *Process) Resume() (proc.Thread, error) {
	return nil, ErrContinueCore
}

// StepInstruction will always return an error
// as you cannot control execution of a core file.
func (p *Process) StepInstruction() error {
	return ErrContinueCore
}

// RequestManualStop will return nil and have no effect
// as you cannot control execution of a core file.
func (p *Process) RequestManualStop() error {
	return nil
}

// CheckAndClearManualStopRequest will always return false and
// have no effect since there are no manual stop requests as
// there is no controlling execution of a core file.
func (p *Process) CheckAndClearManualStopRequest() bool {
	return false
}

// Detach will always return nil and have no
// effect as you cannot detach from a core file
// and have it continue execution or exit.
func (p *Process) Detach(bool) error {
	return nil
}

// Valid returns whether the process is active. Always returns true
// for core files as it cannot exit or be otherwise detached from.
func (p *Process) Valid() (bool, error) {
	return true, nil
}

// Common returns common information across Process
// implementations.
func (p *Process) Common() *proc.CommonProcess {
	return &p.common
}

// Pid returns the process ID of this process.
func (p *Process) Pid() int {
	return p.pid
}

// ResumeNotify is a no-op on core files as we cannot
// control execution.
func (p *Process) ResumeNotify(chan<- struct{}) {
}

// ThreadList will return a list of all threads currently in the process.
func (p *Process) ThreadList() []proc.Thread {
	r := make([]proc.Thread, 0, len(p.Threads))
	for _, v := range p.Threads {
		r = append(r, v)
	}
	return r
}

// FindThread will return the thread with the corresponding thread ID.
func (p *Process) FindThread(threadID int) (proc.Thread, bool) {
	t, ok := p.Threads[threadID]
	return t, ok
}
