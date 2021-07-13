package proc

import (
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"github.com/go-delve/delve/pkg/goversion"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func ptrSizeByRuntimeArch() int {
	return int(unsafe.Sizeof(uintptr(0)))
}

func TestIssue554(t *testing.T) {
	// unsigned integer overflow in proc.(*memCache).contains was
	// causing it to always return true for address 0xffffffffffffffff
	mem := memCache{true, 0x20, make([]byte, 100), nil}
	var addr uint64
	switch ptrSizeByRuntimeArch() {
	case 4:
		addr = 0xffffffff
	case 8:
		addr = 0xffffffffffffffff
	}
	if mem.contains(addr, 40) {
		t.Fatalf("should be false")
	}
}

type dummyMem struct {
	t     *testing.T
	mem   []byte
	base  uint64
	reads []memRead
}

type memRead struct {
	addr uint64
	size int
}

func (dm *dummyMem) ReadMemory(buf []byte, addr uint64) (int, error) {
	dm.t.Logf("read addr=%#x size=%#x\n", addr, len(buf))
	dm.reads = append(dm.reads, memRead{uint64(addr), len(buf)})
	a := int64(addr) - int64(dm.base)
	if a < 0 {
		panic("reading below base")
	}
	if int(a)+len(buf) > len(dm.mem) {
		panic("reading beyond end of mem")
	}
	copy(buf, dm.mem[a:])
	return len(buf), nil
}

func (dm *dummyMem) WriteMemory(uint64, []byte) (int, error) {
	panic("not supported")
}

func TestReadCStringValue(t *testing.T) {
	const tgt = "a test string"
	const maxstrlen = 64

	dm := &dummyMem{t: t}
	dm.mem = make([]byte, maxstrlen)
	copy(dm.mem, tgt)

	for _, tc := range []struct {
		base     uint64
		numreads int
	}{
		{0x5000, 1},
		{0x5001, 1},
		{0x4fff, 2},
		{uint64(0x5000 - len(tgt) - 1), 1},
		{uint64(0x5000-len(tgt)-1) + 1, 2},
	} {
		t.Logf("base is %#x\n", tc.base)
		dm.base = tc.base
		dm.reads = dm.reads[:0]
		out, done, err := readCStringValue(dm, tc.base, LoadConfig{MaxStringLen: maxstrlen})
		if err != nil {
			t.Errorf("base=%#x readCStringValue: %v", tc.base, err)
		}
		if !done {
			t.Errorf("base=%#x expected done but wasn't", tc.base)
		}
		if out != tgt {
			t.Errorf("base=%#x got %q expected %q", tc.base, out, tgt)
		}
		if len(dm.reads) != tc.numreads {
			t.Errorf("base=%#x wrong number of reads %d (expected %d)", tc.base, len(dm.reads), tc.numreads)
		}
		if tc.base == 0x4fff && dm.reads[0].size != 1 {
			t.Errorf("base=%#x first read in not of one byte", tc.base)
		}
	}
}

func assertNoError(err error, t testing.TB, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func TestDwarfVersion(t *testing.T) {
	// Tests that we correctly read the version of compilation units
	fixture := protest.BuildFixture("math", 0)
	bi := NewBinaryInfo(runtime.GOOS, runtime.GOARCH)
	assertNoError(bi.LoadBinaryInfo(fixture.Path, 0, nil), t, "LoadBinaryInfo")
	for _, cu := range bi.Images[0].compileUnits {
		if cu.Version != 4 {
			t.Errorf("compile unit %q at %#x has bad version %d", cu.name, cu.entry.Offset, cu.Version)
		}
	}
}

func TestRegabiFlagSentinel(t *testing.T) {
	// Detect if the regabi flag in the producer string gets removed
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 17) || runtime.GOARCH != "amd64" {
		t.Skip("irrelevant before Go 1.17 or on non-amd64 architectures")
	}
	fixture := protest.BuildFixture("math", 0)
	bi := NewBinaryInfo(runtime.GOOS, runtime.GOARCH)
	assertNoError(bi.LoadBinaryInfo(fixture.Path, 0, nil), t, "LoadBinaryInfo")
	if !bi.regabi {
		t.Errorf("regabi flag not set")
	}
}
