package proc_test

import (
	"encoding/binary"
	"encoding/gob"
	"flag"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/core"

	protest "github.com/go-delve/delve/pkg/proc/test"
)

var fuzzEvalExpressionSetup = flag.Bool("fuzzevalexpressionsetup", false, "Performs setup for FuzzEvalExpression")

const (
	fuzzExecutable = "testdata/fuzzexe"
	fuzzCoredump   = "testdata/fuzzcoredump"
	fuzzInfoPath   = "testdata/fuzzinfo"
)

type fuzzInfo struct {
	Loc       *proc.Location
	Memchunks []memchunk
	Regs      op.DwarfRegisters
	Fuzzbuf   []byte
}

// FuzzEvalExpression fuzzes the variables loader and expression evaluator of Delve.
// To run it, execute the setup first:
//
//	go test -run FuzzEvalExpression -fuzzevalexpressionsetup
//
// this will create some required files in testdata, the fuzzer can then be run with:
//
//	go test -run NONE -fuzz FuzzEvalExpression -v -fuzzminimizetime=0
func FuzzEvalExpression(f *testing.F) {
	if *fuzzEvalExpressionSetup {
		doFuzzEvalExpressionSetup(f)
	}
	_, err := os.Stat(fuzzExecutable)
	if os.IsNotExist(err) {
		f.Skip("not setup")
	}
	bi := proc.NewBinaryInfo("linux", "amd64")
	assertNoError(bi.LoadBinaryInfo(fuzzExecutable, 0, nil), f, "LoadBinaryInfo")
	fh, err := os.Open(fuzzInfoPath)
	assertNoError(err, f, "Open fuzzInfoPath")
	defer fh.Close()
	var fi fuzzInfo
	gob.NewDecoder(fh).Decode(&fi)
	fi.Regs.ByteOrder = binary.LittleEndian
	fns, err := bi.FindFunction("main.main")
	assertNoError(err, f, "FindFunction main.main")
	fi.Loc.Fn = fns[0]
	f.Add(fi.Fuzzbuf)
	f.Fuzz(func(t *testing.T, fuzzbuf []byte) {
		t.Log("fuzzbuf len", len(fuzzbuf))
		mem := &core.SplicedMemory{}

		// can't work with shrunk input fuzzbufs provided by the fuzzer, resize it
		// so it is always at least the size we want.
		lastMemchunk := fi.Memchunks[len(fi.Memchunks)-1]
		fuzzbufsz := lastMemchunk.Idx + int(lastMemchunk.Sz)
		if fuzzbufsz > len(fuzzbuf) {
			newfuzzbuf := make([]byte, fuzzbufsz)
			copy(newfuzzbuf, fuzzbuf)
			fuzzbuf = newfuzzbuf
		}

		end := uint64(0)

		for _, memchunk := range fi.Memchunks {
			if end != memchunk.Addr {
				mem.Add(&zeroReader{}, end, memchunk.Addr-end)
			}
			mem.Add(&offsetReader{fuzzbuf[memchunk.Idx:][:memchunk.Sz], memchunk.Addr}, memchunk.Addr, memchunk.Sz)
			end = memchunk.Addr + memchunk.Sz
		}

		scope := &proc.EvalScope{Location: *fi.Loc, Regs: fi.Regs, Mem: memoryReaderWithFailingWrites{mem}, BinInfo: bi}
		for _, tc := range getEvalExpressionTestCases() {
			scope.EvalExpression(tc.name, pnormalLoadConfig)
		}
	})
}

func doFuzzEvalExpressionSetup(f *testing.F) {
	os.Mkdir("testdata", 0700)
	withTestProcess("testvariables2", f, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		exePath := fixture.Path
		assertNoError(grp.Continue(), f, "Continue")
		fh, err := os.Create(fuzzCoredump)
		assertNoError(err, f, "Creating coredump")
		var state proc.DumpState
		p.Dump(fh, 0, &state)
		assertNoError(state.Err, f, "Dump()")
		out, err := exec.Command("cp", exePath, fuzzExecutable).CombinedOutput()
		f.Log(string(out))
		assertNoError(err, f, "cp")
	})

	// 2. Open the core file and search for the correct goroutine

	cgrp, err := core.OpenCore(fuzzCoredump, fuzzExecutable, nil)
	c := cgrp.Selected
	assertNoError(err, f, "OpenCore")
	gs, _, err := proc.GoroutinesInfo(c, 0, 0)
	assertNoError(err, f, "GoroutinesInfo")
	found := false
	for _, g := range gs {
		if strings.Contains(g.UserCurrent().File, "testvariables2") {
			c.SwitchGoroutine(g)
			found = true
			break
		}
	}
	if !found {
		f.Errorf("could not find testvariables2 goroutine")
	}

	// 3. Run all the test cases on the core file, register which memory addresses are read

	frames, err := c.SelectedGoroutine().Stacktrace(2, 0)
	assertNoError(err, f, "Stacktrace")

	mem := c.Memory()
	loc, _ := c.CurrentThread().Location()
	tmem := &tracingMem{make(map[uint64]int), mem}

	scope := &proc.EvalScope{Location: *loc, Regs: frames[0].Regs, Mem: tmem, BinInfo: c.BinInfo()}

	for _, tc := range getEvalExpressionTestCases() {
		scope.EvalExpression(tc.name, pnormalLoadConfig)
	}

	// 4. Copy all the memory we read into a buffer to use as fuzz example (if
	// we try to use the whole core dump as fuzz example the Go fuzzer crashes)

	memchunks, fuzzbuf := tmem.memoryReadsCondensed()

	fh, err := os.Create(fuzzInfoPath)
	assertNoError(err, f, "os.Create")
	frames[0].Regs.ByteOrder = nil
	loc.Fn = nil
	assertNoError(gob.NewEncoder(fh).Encode(fuzzInfo{
		Loc:       loc,
		Memchunks: memchunks,
		Regs:      frames[0].Regs,
		Fuzzbuf:   fuzzbuf,
	}), f, "Encode")
	assertNoError(fh.Close(), f, "Close")
}

type tracingMem struct {
	read map[uint64]int
	mem  proc.MemoryReadWriter
}

func (tmem *tracingMem) ReadMemory(b []byte, n uint64) (int, error) {
	if len(b) > tmem.read[n] {
		tmem.read[n] = len(b)
	}
	return tmem.mem.ReadMemory(b, n)
}

func (tmem *tracingMem) WriteMemory(uint64, []byte) (int, error) {
	panic("should not be called")
}

type memchunk struct {
	Addr, Sz uint64
	Idx      int
}

func (tmem *tracingMem) memoryReadsCondensed() ([]memchunk, []byte) {
	memoryReads := make([]memchunk, 0, len(tmem.read))
	for addr, sz := range tmem.read {
		memoryReads = append(memoryReads, memchunk{addr, uint64(sz), 0})
	}
	sort.Slice(memoryReads, func(i, j int) bool { return memoryReads[i].Addr < memoryReads[j].Addr })

	memoryReadsCondensed := make([]memchunk, 0, len(memoryReads))
	for _, v := range memoryReads {
		if len(memoryReadsCondensed) != 0 {
			last := &memoryReadsCondensed[len(memoryReadsCondensed)-1]
			if last.Addr+last.Sz >= v.Addr {
				end := v.Addr + v.Sz
				sz2 := end - last.Addr
				if sz2 > last.Sz {
					last.Sz = sz2
				}
				continue
			}
		}
		memoryReadsCondensed = append(memoryReadsCondensed, v)
	}

	fuzzbuf := []byte{}
	for i := range memoryReadsCondensed {
		buf := make([]byte, memoryReadsCondensed[i].Sz)
		tmem.mem.ReadMemory(buf, memoryReadsCondensed[i].Addr)
		memoryReadsCondensed[i].Idx = len(fuzzbuf)
		fuzzbuf = append(fuzzbuf, buf...)
	}

	return memoryReadsCondensed, fuzzbuf
}

type offsetReader struct {
	buf  []byte
	addr uint64
}

func (or *offsetReader) ReadMemory(buf []byte, addr uint64) (int, error) {
	copy(buf, or.buf[addr-or.addr:][:len(buf)])
	return len(buf), nil
}

type memoryReaderWithFailingWrites struct {
	proc.MemoryReader
}

func (w memoryReaderWithFailingWrites) WriteMemory(uint64, []byte) (int, error) {
	panic("should not be called")
}

type zeroReader struct{}

func (*zeroReader) ReadMemory(b []byte, addr uint64) (int, error) {
	for i := range b {
		b[i] = 0
	}
	return len(b), nil
}

func (*zeroReader) WriteMemory(b []byte, addr uint64) (int, error) {
	panic("should not be called")
}
