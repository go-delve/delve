package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/elfwriter"
	"github.com/go-delve/delve/pkg/proc"
)

func platformFromNotes(notes []*note) (goos, goarch string, err error) {
	for _, note := range notes {
		if note.Type != elfwriter.DelveHeaderNoteType {
			continue
		}
		lines := strings.Split(string(note.Desc.([]byte)), "\n")
		v := strings.Split(lines[0], "/")
		if len(v) != 2 {
			return "", "", fmt.Errorf("malformed delve header note: %q", string(note.Desc.([]byte)))
		}
		return v[0], v[1], nil
	}
	panic("internal error")
}

func threadsFromDelveNotes(p *process, notes []*note) (proc.Thread, error) {
	var currentThread proc.Thread
	for _, note := range notes {
		if note.Type == elfwriter.DelveHeaderNoteType {
			buf := bytes.NewBuffer(note.Desc.([]byte))
			for {
				line, err := buf.ReadString('\n')
				if err != nil {
					break
				}
				if len(line) > 0 && line[len(line)-1] == '\n' {
					line = line[:len(line)-1]
				}
				switch {
				case strings.HasPrefix(line, elfwriter.DelveHeaderTargetPidPrefix):
					pid, err := strconv.ParseUint(line[len(elfwriter.DelveHeaderTargetPidPrefix):], 10, 64)
					if err != nil {
						return nil, fmt.Errorf("malformed delve header note (bad pid): %v", err)
					}
					p.pid = int(pid)
				case strings.HasPrefix(line, elfwriter.DelveHeaderEntryPointPrefix):
					entry, err := strconv.ParseUint(line[len(elfwriter.DelveHeaderEntryPointPrefix):], 0, 64)
					if err != nil {
						return nil, fmt.Errorf("malformed delve header note (bad entry point): %v", err)
					}
					p.entryPoint = entry
				}
			}
		}

		if note.Type != elfwriter.DelveThreadNodeType {
			continue
		}
		body := bytes.NewReader(note.Desc.([]byte))
		th := new(delveThread)
		th.regs = new(delveRegisters)

		var readerr error
		read := func(out interface{}) {
			if readerr != nil {
				return
			}
			readerr = binary.Read(body, binary.LittleEndian, out)
		}

		read(&th.id)

		read(&th.regs.pc)
		read(&th.regs.sp)
		read(&th.regs.bp)
		read(&th.regs.tls)
		read(&th.regs.hasGAddr)
		read(&th.regs.gaddr)

		var n uint32
		read(&n)

		if readerr != nil {
			return nil, fmt.Errorf("error reading thread note header for thread %d: %v", th.id, readerr)
		}

		th.regs.slice = make([]proc.Register, n)

		readBytes := func(maxlen uint16, kind string) []byte {
			if readerr != nil {
				return nil
			}
			var len uint16
			read(&len)
			if maxlen > 0 && len > maxlen {
				readerr = fmt.Errorf("maximum len exceeded (%d) reading %s", len, kind)
				return nil
			}
			buf := make([]byte, len)
			if readerr != nil {
				return nil
			}
			_, readerr = body.Read(buf)
			return buf
		}

		for i := 0; i < int(n); i++ {
			name := string(readBytes(20, "register name"))
			value := readBytes(2048, "register value")
			th.regs.slice[i] = proc.Register{Name: name, Reg: op.DwarfRegisterFromBytes(value)}
			if readerr != nil {
				return nil, fmt.Errorf("error reading thread note registers for thread %d: %v", th.id, readerr)
			}
		}

		p.Threads[int(th.id)] = &thread{th, p, proc.CommonThread{}}
		if currentThread == nil {
			currentThread = p.Threads[int(th.id)]
		}
	}
	return currentThread, nil
}

type delveThread struct {
	id   uint64
	regs *delveRegisters
}

func (th *delveThread) pid() int {
	return int(th.id)
}

func (th *delveThread) registers() (proc.Registers, error) {
	return th.regs, nil
}

type delveRegisters struct {
	pc, sp, bp, tls uint64
	hasGAddr        bool
	gaddr           uint64
	slice           []proc.Register
}

func (regs *delveRegisters) PC() uint64            { return regs.pc }
func (regs *delveRegisters) BP() uint64            { return regs.bp }
func (regs *delveRegisters) SP() uint64            { return regs.sp }
func (regs *delveRegisters) TLS() uint64           { return regs.tls }
func (regs *delveRegisters) GAddr() (uint64, bool) { return regs.gaddr, regs.hasGAddr }
func (regs *delveRegisters) LR() uint64            { return 0 }

func (regs *delveRegisters) Copy() (proc.Registers, error) {
	return regs, nil
}

func (regs *delveRegisters) Slice(bool) ([]proc.Register, error) {
	return regs.slice, nil
}
