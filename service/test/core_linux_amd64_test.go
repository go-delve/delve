package servicetest

import (
	"fmt"
	"io/ioutil"
	"os/exec"
	"path"
	"testing"

	"strings"

	"path/filepath"

	"github.com/derekparker/delve/pkg/proc"
	"github.com/derekparker/delve/pkg/proc/test"
	"github.com/derekparker/delve/service/api"
)

func TestCore(t *testing.T) {
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
	if err != nil || len(cores) != 1 {
		t.Fatalf("Got %v, wanted one file named core* in %v", cores, tempDir)
	}
	corePath := cores[0]

	p, err := proc.ReadCore(corePath, fix.Path)
	if err != nil {
		pat, err := ioutil.ReadFile("/proc/sys/kernel/core_pattern")
		t.Errorf("read core_pattern: %q, %v", pat, err)
		apport, err := ioutil.ReadFile("/var/log/apport.log")
		t.Errorf("read apport log: %q, %v", apport, err)
		t.Fatalf("ReadCore() failed: %v", err)
	}
	gs, err := p.GoroutinesInfo()
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
	msg, err := mainFrame.Scope(p.CurrentThread()).EvalVariable("msg", proc.LoadConfig{MaxStringLen: 64})
	if err != nil {
		t.Fatalf("Couldn't EvalVariable(msg, ...): %v", err)
	}
	cv := api.ConvertVar(msg)
	if cv.Value != "BOOM!" {
		t.Errorf("main.msg = %q, want %q", cv.Value, "BOOM!")
	}
}
