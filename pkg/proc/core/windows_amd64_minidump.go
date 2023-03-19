package core

import (
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/core/minidump"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

func readAMD64Minidump(minidumpPath, exePath string) (*process, proc.Thread, error) {
	var logfn func(string, ...interface{})
	if logflags.Minidump() {
		logfn = logflags.MinidumpLogger().Infof
	}

	mdmp, err := minidump.Open(minidumpPath, logfn)
	if err != nil {
		if _, isNotAMinidump := err.(minidump.ErrNotAMinidump); isNotAMinidump {
			return nil, nil, ErrUnrecognizedFormat
		}
		return nil, nil, err
	}

	memory := &SplicedMemory{}

	for i := range mdmp.MemoryRanges {
		m := &mdmp.MemoryRanges[i]
		memory.Add(m, m.Addr, uint64(len(m.Data)))
	}

	entryPoint := uint64(0)
	if len(mdmp.Modules) > 0 {
		entryPoint = mdmp.Modules[0].BaseOfImage
	}

	p := &process{
		mem:         memory,
		Threads:     map[int]*thread{},
		bi:          proc.NewBinaryInfo("windows", "amd64"),
		entryPoint:  entryPoint,
		breakpoints: proc.NewBreakpointMap(),
		pid:         int(mdmp.Pid),
	}

	for i := range mdmp.Threads {
		th := &mdmp.Threads[i]
		p.Threads[int(th.ID)] = &thread{&windowsAMD64Thread{th}, p, proc.CommonThread{}}
	}
	var currentThread proc.Thread
	if len(mdmp.Threads) > 0 {
		currentThread = p.Threads[int(mdmp.Threads[0].ID)]
	}
	return p, currentThread, nil
}

type windowsAMD64Thread struct {
	th *minidump.Thread
}

func (th *windowsAMD64Thread) pid() int {
	return int(th.th.ID)
}

func (th *windowsAMD64Thread) registers() (proc.Registers, error) {
	return winutil.NewAMD64Registers(&th.th.Context, th.th.TEB), nil
}
