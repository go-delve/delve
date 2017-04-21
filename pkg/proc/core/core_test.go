package core

import (
	"bytes"
	"fmt"
	"go/constant"
	"io/ioutil"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/derekparker/delve/pkg/proc"
	"github.com/derekparker/delve/pkg/proc/test"
)

func TestSplicedReader(t *testing.T) {
	data := []byte{}
	data2 := []byte{}
	for i := 0; i < 100; i++ {
		data = append(data, byte(i))
		data2 = append(data2, byte(i+100))
	}

	type region struct {
		data   []byte
		off    uintptr
		length uintptr
	}
	tests := []struct {
		name     string
		regions  []region
		readAddr uintptr
		readLen  int
		want     []byte
	}{
		{
			"Insert after",
			[]region{
				{data, 0, 1},
				{data2, 1, 1},
			},
			0,
			2,
			[]byte{0, 101},
		},
		{
			"Insert before",
			[]region{
				{data, 1, 1},
				{data2, 0, 1},
			},
			0,
			2,
			[]byte{100, 1},
		},
		{
			"Completely overwrite",
			[]region{
				{data, 1, 1},
				{data2, 0, 3},
			},
			0,
			3,
			[]byte{100, 101, 102},
		},
		{
			"Overwrite end",
			[]region{
				{data, 0, 2},
				{data2, 1, 2},
			},
			0,
			3,
			[]byte{0, 101, 102},
		},
		{
			"Overwrite start",
			[]region{
				{data, 0, 3},
				{data2, 0, 2},
			},
			0,
			3,
			[]byte{100, 101, 2},
		},
		{
			"Punch hole",
			[]region{
				{data, 0, 5},
				{data2, 1, 3},
			},
			0,
			5,
			[]byte{0, 101, 102, 103, 4},
		},
		{
			"Overlap two",
			[]region{
				{data, 10, 4},
				{data, 14, 4},
				{data2, 12, 4},
			},
			10,
			8,
			[]byte{10, 11, 112, 113, 114, 115, 16, 17},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mem := &SplicedMemory{}
			for _, region := range test.regions {
				r := bytes.NewReader(region.data)
				mem.Add(&OffsetReaderAt{r, 0}, region.off, region.length)
			}
			got := make([]byte, test.readLen)
			n, err := mem.ReadMemory(got, test.readAddr)
			if n != test.readLen || err != nil || !reflect.DeepEqual(got, test.want) {
				t.Errorf("ReadAt = %v, %v, %v, want %v, %v, %v", n, err, got, test.readLen, nil, test.want)
			}
		})
	}
}

func TestCore(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return
	}
	// This is all very fragile and won't work on hosts with non-default core patterns.
	// Might be better to check in the binary and core?
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	fix := test.BuildFixture("panic")
	bashCmd := fmt.Sprintf("cd %v && ulimit -c unlimited && GOTRACEBACK=crash %v", tempDir, fix.Path)
	exec.Command("bash", "-c", bashCmd).Run()
	cores, err := filepath.Glob(path.Join(tempDir, "core*"))
	switch {
	case err != nil || len(cores) > 1:
		t.Fatalf("Got %v, wanted one file named core* in %v", cores, tempDir)
	case len(cores) == 0:
		t.Logf("core file was not produced, could not run test")
		return
	}
	corePath := cores[0]

	p, err := OpenCore(corePath, fix.Path)
	if err != nil {
		pat, err := ioutil.ReadFile("/proc/sys/kernel/core_pattern")
		t.Errorf("read core_pattern: %q, %v", pat, err)
		apport, err := ioutil.ReadFile("/var/log/apport.log")
		t.Errorf("read apport log: %q, %v", apport, err)
		t.Fatalf("ReadCore() failed: %v", err)
	}
	gs, err := proc.GoroutinesInfo(p)
	if err != nil || len(gs) == 0 {
		t.Fatalf("GoroutinesInfo() = %v, %v; wanted at least one goroutine", gs, err)
	}

	var panicking *proc.G
	var panickingStack []proc.Stackframe
	for _, g := range gs {
		stack, err := g.Stacktrace(10)
		if err != nil {
			t.Errorf("Stacktrace() on goroutine %v = %v", g, err)
		}
		for _, frame := range stack {
			if strings.Contains(frame.Current.Fn.Name, "panic") {
				panicking = g
				panickingStack = stack
			}
		}
	}
	if panicking == nil {
		t.Fatalf("Didn't find a call to panic in goroutine stacks: %v", gs)
	}

	var mainFrame *proc.Stackframe
	// Walk backward, because the current function seems to be main.main
	// in the actual call to panic().
	for i := len(panickingStack) - 1; i >= 0; i-- {
		if panickingStack[i].Current.Fn.Name == "main.main" {
			mainFrame = &panickingStack[i]
		}
	}
	if mainFrame == nil {
		t.Fatalf("Couldn't find main in stack %v", panickingStack)
	}
	msg, err := proc.FrameToScope(p, *mainFrame).EvalVariable("msg", proc.LoadConfig{MaxStringLen: 64})
	if err != nil {
		t.Fatalf("Couldn't EvalVariable(msg, ...): %v", err)
	}
	if constant.StringVal(msg.Value) != "BOOM!" {
		t.Errorf("main.msg = %q, want %q", msg.Value, "BOOM!")
	}

	regs, err := p.CurrentThread().Registers(true)
	if err != nil {
		t.Fatalf("Couldn't get current thread registers: %v", err)
	}
	regslice := regs.Slice()
	for _, reg := range regslice {
		t.Logf("%s = %s", reg.Name, reg.Value)
	}
}
