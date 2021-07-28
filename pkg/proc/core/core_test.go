package core

import (
	"bytes"
	"flag"
	"fmt"
	"go/constant"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/test"
)

var buildMode string

func TestMain(m *testing.M) {
	flag.StringVar(&buildMode, "test-buildmode", "", "selects build mode")
	flag.Parse()
	if buildMode != "" && buildMode != "pie" {
		fmt.Fprintf(os.Stderr, "unknown build mode %q", buildMode)
		os.Exit(1)
	}
	os.Exit(test.RunTestsWithFixtures(m))
}

func assertNoError(err error, t testing.TB, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func TestSplicedReader(t *testing.T) {
	data := []byte{}
	data2 := []byte{}
	for i := 0; i < 100; i++ {
		data = append(data, byte(i))
		data2 = append(data2, byte(i+100))
	}

	type region struct {
		data   []byte
		off    uint64
		length uint64
	}
	tests := []struct {
		name     string
		regions  []region
		readAddr uint64
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
			mem := &splicedMemory{}
			for _, region := range test.regions {
				r := bytes.NewReader(region.data)
				mem.Add(&offsetReaderAt{r, 0}, region.off, region.length)
			}
			got := make([]byte, test.readLen)
			n, err := mem.ReadMemory(got, test.readAddr)
			if n != test.readLen || err != nil || !reflect.DeepEqual(got, test.want) {
				t.Errorf("ReadAt = %v, %v, %v, want %v, %v, %v", n, err, got, test.readLen, nil, test.want)
			}
		})
	}

	// Test some ReadMemory errors

	mem := &splicedMemory{}
	for _, region := range []region{
		{[]byte{0xa1, 0xa2, 0xa3, 0xa4}, 0x1000, 4},
		{[]byte{0xb1, 0xb2, 0xb3, 0xb4}, 0x1004, 4},
		{[]byte{0xc1, 0xc2, 0xc3, 0xc4}, 0x1010, 4},
	} {
		r := bytes.NewReader(region.data)
		mem.Add(&offsetReaderAt{r, region.off}, region.off, region.length)
	}

	got := make([]byte, 4)

	// Read before the first mapping
	_, err := mem.ReadMemory(got, 0x900)
	if err == nil || !strings.HasPrefix(err.Error(), "error while reading spliced memory at 0x900") {
		t.Errorf("Read before the start of memory didn't fail (or wrong error): %v", err)
	}

	// Read after the last mapping
	_, err = mem.ReadMemory(got, 0x1100)
	if err == nil || (err.Error() != "offset 4352 did not match any regions") {
		t.Errorf("Read after the end of memory didn't fail (or wrong error): %v", err)
	}

	// Read at the start of the first entry
	_, err = mem.ReadMemory(got, 0x1000)
	if err != nil || !bytes.Equal(got, []byte{0xa1, 0xa2, 0xa3, 0xa4}) {
		t.Errorf("Reading at the start of the first entry: %v %#x", err, got)
	}

	// Read at the start of the second entry
	_, err = mem.ReadMemory(got, 0x1004)
	if err != nil || !bytes.Equal(got, []byte{0xb1, 0xb2, 0xb3, 0xb4}) {
		t.Errorf("Reading at the start of the second entry: %v %#x", err, got)
	}

	// Read straddling entries 1 and 2
	_, err = mem.ReadMemory(got, 0x1002)
	if err != nil || !bytes.Equal(got, []byte{0xa3, 0xa4, 0xb1, 0xb2}) {
		t.Errorf("Straddled read of the second entry: %v %#x", err, got)
	}

	// Read past the end of the second entry
	_, err = mem.ReadMemory(got, 0x1007)
	if err == nil || !strings.HasPrefix(err.Error(), "error while reading spliced memory at 0x1008") {
		t.Errorf("Read into gap: %v", err)
	}
}

func withCoreFile(t *testing.T, name, args string) *proc.Target {
	// This is all very fragile and won't work on hosts with non-default core patterns.
	// Might be better to check in the binary and core?
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	test.PathsToRemove = append(test.PathsToRemove, tempDir)
	var buildFlags test.BuildFlags
	if buildMode == "pie" {
		buildFlags = test.BuildModePIE
	}
	fix := test.BuildFixture(name, buildFlags)
	bashCmd := fmt.Sprintf("cd %v && ulimit -c unlimited && GOTRACEBACK=crash %v %s", tempDir, fix.Path, args)
	exec.Command("bash", "-c", bashCmd).Run()
	cores, err := filepath.Glob(path.Join(tempDir, "core*"))
	switch {
	case err != nil || len(cores) > 1:
		t.Fatalf("Got %v, wanted one file named core* in %v", cores, tempDir)
	case len(cores) == 0:
		t.Skipf("core file was not produced, could not run test")
		return nil
	}
	corePath := cores[0]

	p, err := OpenCore(corePath, fix.Path, []string{})
	if err != nil {
		t.Errorf("OpenCore(%q) failed: %v", corePath, err)
		pat, err := ioutil.ReadFile("/proc/sys/kernel/core_pattern")
		t.Errorf("read core_pattern: %q, %v", pat, err)
		apport, err := ioutil.ReadFile("/var/log/apport.log")
		t.Errorf("read apport log: %q, %v", apport, err)
		t.Fatalf("previous errors")
	}
	return p
}

func logRegisters(t *testing.T, regs proc.Registers, arch *proc.Arch) {
	dregs := arch.RegistersToDwarfRegisters(0, regs)
	dregs.Reg(^uint64(0))
	for i := 0; i < dregs.CurrentSize(); i++ {
		reg := dregs.Reg(uint64(i))
		if reg == nil {
			continue
		}
		name, _, value := arch.DwarfRegisterToString(i, reg)
		t.Logf("%s = %s", name, value)
	}
}

func TestCore(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return
	}
	if runtime.GOOS == "linux" && os.Getenv("CI") == "true" && buildMode == "pie" {
		t.Skip("disabled on linux, Github Actions, with PIE buildmode")
	}
	p := withCoreFile(t, "panic", "")

	gs, _, err := proc.GoroutinesInfo(p, 0, 0)
	if err != nil || len(gs) == 0 {
		t.Fatalf("GoroutinesInfo() = %v, %v; wanted at least one goroutine", gs, err)
	}

	var panicking *proc.G
	var panickingStack []proc.Stackframe
	for _, g := range gs {
		t.Logf("Goroutine %d", g.ID)
		stack, err := g.Stacktrace(10, 0)
		if err != nil {
			t.Errorf("Stacktrace() on goroutine %v = %v", g, err)
		}
		for _, frame := range stack {
			fnname := ""
			if frame.Call.Fn != nil {
				fnname = frame.Call.Fn.Name
			}
			t.Logf("\tframe %s:%d in %s %#x (systemstack: %v)", frame.Call.File, frame.Call.Line, fnname, frame.Call.PC, frame.SystemStack)
			if frame.Current.Fn != nil && strings.Contains(frame.Current.Fn.Name, "panic") {
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
		if panickingStack[i].Current.Fn != nil && panickingStack[i].Current.Fn.Name == "main.main" {
			mainFrame = &panickingStack[i]
		}
	}
	if mainFrame == nil {
		t.Fatalf("Couldn't find main in stack %v", panickingStack)
	}
	msg, err := proc.FrameToScope(p, p.Memory(), nil, *mainFrame).EvalVariable("msg", proc.LoadConfig{MaxStringLen: 64})
	if err != nil {
		t.Fatalf("Couldn't EvalVariable(msg, ...): %v", err)
	}
	if constant.StringVal(msg.Value) != "BOOM!" {
		t.Errorf("main.msg = %q, want %q", msg.Value, "BOOM!")
	}

	regs, err := p.CurrentThread().Registers()
	if err != nil {
		t.Fatalf("Couldn't get current thread registers: %v", err)
	}
	logRegisters(t, regs, p.BinInfo().Arch)
}

func TestCoreFpRegisters(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return
	}
	// in go1.10 the crash is executed on a different thread and registers are
	// no longer available in the core dump.
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		t.Skip("not supported in go1.10 and later")
	}

	p := withCoreFile(t, "fputest/", "panic")

	gs, _, err := proc.GoroutinesInfo(p, 0, 0)
	if err != nil || len(gs) == 0 {
		t.Fatalf("GoroutinesInfo() = %v, %v; wanted at least one goroutine", gs, err)
	}

	var regs proc.Registers
	for _, thread := range p.ThreadList() {
		frames, err := proc.ThreadStacktrace(thread, 10)
		if err != nil {
			t.Errorf("ThreadStacktrace for %x = %v", thread.ThreadID(), err)
			continue
		}
		for i := range frames {
			if frames[i].Current.Fn == nil {
				continue
			}
			if frames[i].Current.Fn.Name == "main.main" {
				regs, err = thread.Registers()
				if err != nil {
					t.Fatalf("Could not get registers for thread %x, %v", thread.ThreadID(), err)
				}
				break
			}
		}
		if regs != nil {
			break
		}
	}

	regtests := []struct{ name, value string }{
		{"ST(0)", "0x3fffe666660000000000"},
		{"ST(1)", "0x3fffd9999a0000000000"},
		{"ST(2)", "0x3fffcccccd0000000000"},
		{"ST(3)", "0x3fffc000000000000000"},
		{"ST(4)", "0x3fffb333333333333000"},
		{"ST(5)", "0x3fffa666666666666800"},
		{"ST(6)", "0x3fff9999999999999800"},
		{"ST(7)", "0x3fff8cccccccccccd000"},
		// Unlike TestClientServer_FpRegisters in service/test/integration2_test
		// we can not test the value of XMM0, it probably has been reused by
		// something between the panic and the time we get the core dump.
		{"XMM9", "0x3ff66666666666663ff4cccccccccccd"},
		{"XMM10", "0x3fe666663fd9999a3fcccccd3fc00000"},
		{"XMM3", "0x3ff199999999999a3ff3333333333333"},
		{"XMM4", "0x3ff4cccccccccccd3ff6666666666666"},
		{"XMM5", "0x3fcccccd3fc000003fe666663fd9999a"},
		{"XMM6", "0x4004cccccccccccc4003333333333334"},
		{"XMM7", "0x40026666666666664002666666666666"},
		{"XMM8", "0x4059999a404ccccd4059999a404ccccd"},
	}

	arch := p.BinInfo().Arch
	logRegisters(t, regs, arch)
	dregs := arch.RegistersToDwarfRegisters(0, regs)

	for _, regtest := range regtests {
		found := false
		dregs.Reg(^uint64(0))
		for i := 0; i < dregs.CurrentSize(); i++ {
			reg := dregs.Reg(uint64(i))
			regname, _, regval := arch.DwarfRegisterToString(i, reg)
			if reg != nil && regname == regtest.name {
				found = true
				if !strings.HasPrefix(regval, regtest.value) {
					t.Fatalf("register %s expected %q got %q", regname, regtest.value, regval)
				}
			}
		}
		if !found {
			t.Fatalf("register %s not found: %v", regtest.name, regs)
		}
	}
}

func TestCoreWithEmptyString(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return
	}
	if runtime.GOOS == "linux" && os.Getenv("CI") == "true" && buildMode == "pie" {
		t.Skip("disabled on linux, Github Actions, with PIE buildmode")
	}
	p := withCoreFile(t, "coreemptystring", "")

	gs, _, err := proc.GoroutinesInfo(p, 0, 0)
	assertNoError(err, t, "GoroutinesInfo")

	var mainFrame *proc.Stackframe
mainSearch:
	for _, g := range gs {
		stack, err := g.Stacktrace(10, 0)
		assertNoError(err, t, "Stacktrace()")
		for _, frame := range stack {
			if frame.Current.Fn != nil && frame.Current.Fn.Name == "main.main" {
				mainFrame = &frame
				break mainSearch
			}
		}
	}

	if mainFrame == nil {
		t.Fatal("could not find main.main frame")
	}

	scope := proc.FrameToScope(p, p.Memory(), nil, *mainFrame)
	loadConfig := proc.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}
	v1, err := scope.EvalVariable("t", loadConfig)
	assertNoError(err, t, "EvalVariable(t)")
	assertNoError(v1.Unreadable, t, "unreadable variable 't'")
	t.Logf("t = %#v\n", v1)
	v2, err := scope.EvalVariable("s", loadConfig)
	assertNoError(err, t, "EvalVariable(s)")
	assertNoError(v2.Unreadable, t, "unreadable variable 's'")
	t.Logf("s = %#v\n", v2)
}

func TestMinidump(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("minidumps can only be produced on windows")
	}
	var buildFlags test.BuildFlags
	if buildMode == "pie" {
		buildFlags = test.BuildModePIE
	}
	fix := test.BuildFixture("sleep", buildFlags)
	mdmpPath := procdump(t, fix.Path)

	p, err := OpenCore(mdmpPath, fix.Path, []string{})
	if err != nil {
		t.Fatalf("OpenCore: %v", err)
	}
	gs, _, err := proc.GoroutinesInfo(p, 0, 0)
	if err != nil || len(gs) == 0 {
		t.Fatalf("GoroutinesInfo() = %v, %v; wanted at least one goroutine", gs, err)
	}
	t.Logf("%d goroutines", len(gs))
	foundMain, foundTime := false, false
	for _, g := range gs {
		stack, err := g.Stacktrace(10, 0)
		if err != nil {
			t.Errorf("Stacktrace() on goroutine %v = %v", g, err)
		}
		t.Logf("goroutine %d", g.ID)
		for _, frame := range stack {
			name := "?"
			if frame.Current.Fn != nil {
				name = frame.Current.Fn.Name
			}
			t.Logf("\t%s:%d in %s %#x", frame.Current.File, frame.Current.Line, name, frame.Current.PC)
			if frame.Current.Fn == nil {
				continue
			}
			switch frame.Current.Fn.Name {
			case "main.main":
				foundMain = true
			case "time.Sleep":
				foundTime = true
			}
		}
		if foundMain != foundTime {
			t.Errorf("found main.main but no time.Sleep (or viceversa) %v %v", foundMain, foundTime)
		}
	}
	if !foundMain {
		t.Fatalf("could not find main goroutine")
	}
}

func procdump(t *testing.T, exePath string) string {
	exeDir := filepath.Dir(exePath)
	cmd := exec.Command("procdump64", "-accepteula", "-ma", "-n", "1", "-s", "3", "-x", exeDir, exePath, "quit")
	out, err := cmd.CombinedOutput() // procdump exits with non-zero status on success, so we have to ignore the error here
	if !strings.Contains(string(out), "Dump count reached.") {
		t.Fatalf("possible error running procdump64, output: %q, error: %v", string(out), err)
	}

	dh, err := os.Open(exeDir)
	if err != nil {
		t.Fatalf("could not open executable file directory %q: %v", exeDir, err)
	}
	defer dh.Close()
	fis, err := dh.Readdir(-1)
	if err != nil {
		t.Fatalf("could not read executable file directory %q: %v", exeDir, err)
	}
	t.Logf("looking for dump file")
	exeName := filepath.Base(exePath)
	for _, fi := range fis {
		name := fi.Name()
		t.Logf("\t%s", name)
		if strings.HasPrefix(name, exeName) && strings.HasSuffix(name, ".dmp") {
			mdmpPath := filepath.Join(exeDir, name)
			test.PathsToRemove = append(test.PathsToRemove, mdmpPath)
			return mdmpPath
		}
	}

	t.Fatalf("could not find dump file")
	return ""
}
