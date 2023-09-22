package native

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-delve/delve/pkg/elfwriter"
	"github.com/go-delve/delve/pkg/proc/linutil"
	"golang.org/x/sys/unix"
)

const _NT_AUXV elf.NType = 0x6

type linuxPrPsInfo struct {
	State                uint8
	Sname                int8
	Zomb                 uint8
	Nice                 int8
	_                    [4]uint8
	Flag                 uint64
	Uid, Gid             uint32
	Pid, Ppid, Pgrp, Sid int32
	Fname                [16]uint8
	Args                 [80]uint8
}

func (p *nativeProcess) DumpProcessNotes(notes []elfwriter.Note, threadDone func()) (threadsDone bool, out []elfwriter.Note, err error) {
	tobytes := func(x interface{}) []byte {
		out := new(bytes.Buffer)
		_ = binary.Write(out, binary.LittleEndian, x)
		return out.Bytes()
	}

	prpsinfo := linuxPrPsInfo{
		Pid: int32(p.pid),
	}

	fname := p.os.comm
	if len(fname) > len(prpsinfo.Fname)-1 {
		fname = fname[:len(prpsinfo.Fname)-1]
	}
	copy(prpsinfo.Fname[:], fname)
	prpsinfo.Fname[len(fname)] = 0

	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", p.pid)); err == nil {
		for len(cmdline) > 0 && cmdline[len(cmdline)-1] == '\n' {
			cmdline = cmdline[:len(cmdline)-1]
		}
		if zero := bytes.Index(cmdline, []byte{0}); zero >= 0 {
			cmdline = cmdline[zero+1:]
		}
		path := p.BinInfo().Images[0].Path
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		args := make([]byte, 0, len(path)+len(cmdline)+1)
		args = append(args, []byte(path)...)
		args = append(args, 0)
		args = append(args, cmdline...)
		if len(args) > len(prpsinfo.Args)-1 {
			args = args[:len(prpsinfo.Args)-1]
		}
		copy(prpsinfo.Args[:], args)
		prpsinfo.Args[len(args)] = 0
	}
	notes = append(notes, elfwriter.Note{
		Type: elf.NT_PRPSINFO,
		Data: tobytes(prpsinfo),
	})

	auxvbuf, err := os.ReadFile(fmt.Sprintf("/proc/%d/auxv", p.pid))
	if err == nil {
		notes = append(notes, elfwriter.Note{
			Type: _NT_AUXV,
			Data: auxvbuf,
		})
	}

	for _, th := range p.threads {
		regs, err := th.Registers()
		if err != nil {
			return false, notes, err
		}

		regs, err = regs.Copy() // triggers floating point register load
		if err != nil {
			return false, notes, err
		}

		nregs := regs.(*linutil.AMD64Registers)

		var prstatus linuxPrStatusAMD64
		prstatus.Pid = int32(th.ID)
		prstatus.Ppid = int32(p.pid)
		prstatus.Pgrp = int32(p.pid)
		prstatus.Sid = int32(p.pid)
		prstatus.Reg = *(nregs.Regs)
		notes = append(notes, elfwriter.Note{
			Type: elf.NT_PRSTATUS,
			Data: tobytes(prstatus),
		})

		var xsave []byte

		if nregs.Fpregset != nil && nregs.Fpregset.Xsave != nil {
			xsave = make([]byte, len(nregs.Fpregset.Xsave))
			copy(xsave, nregs.Fpregset.Xsave)
		} else {
			xsave = make([]byte, 512+64) // XSAVE header start + XSAVE header length
		}

		// Even if we have the XSAVE area on some versions of linux (or some CPU
		// models?) it won't contain the legacy x87 registers, so copy them over
		// in case we got them from PTRACE_GETFPREGS.
		buf := new(bytes.Buffer)
		binary.Write(buf, binary.LittleEndian, &nregs.Fpregset.AMD64PtraceFpRegs)
		copy(xsave, buf.Bytes())

		notes = append(notes, elfwriter.Note{
			Type: _NT_X86_XSTATE,
			Data: xsave,
		})

		threadDone()
	}

	return true, notes, nil
}

type linuxPrStatusAMD64 struct {
	Siginfo                      linuxSiginfo
	Cursig                       uint16
	_                            [2]uint8
	Sigpend                      uint64
	Sighold                      uint64
	Pid, Ppid, Pgrp, Sid         int32
	Utime, Stime, CUtime, CStime unix.Timeval
	Reg                          linutil.AMD64PtraceRegs
	Fpvalid                      int64
}

// LinuxSiginfo is a copy of the
// siginfo kernel struct.
type linuxSiginfo struct {
	Signo int32
	Code  int32
	Errno int32
}
