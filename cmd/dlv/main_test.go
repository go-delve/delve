package main

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func buildBinary(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "dbg-test")

	err := cmd.Run()
	if err != nil {
		t.Fatal(err)
	}
}

func startDebugger(t *testing.T, pid int) *os.Process {
	cmd := exec.Command("sudo", "./dbg-test", "-pid", strconv.Itoa(pid))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = bytes.NewBufferString("exit\ny\n")

	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	return cmd.Process
}

func startTestProg(t *testing.T, proc string) *os.Process {
	err := exec.Command("go", "build", "-gcflags=-N -l", "-o", proc, proc+".go").Run()
	if err != nil {
		t.Fatal("Could not compile", proc, err)
	}
	defer os.Remove(proc)
	cmd := exec.Command(proc)

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	return cmd.Process
}

func TestCleanExit(t *testing.T) {
	buildBinary(t)

	var (
		waitchan = make(chan *os.ProcessState)
		testprog = startTestProg(t, "../../_fixtures/livetestprog")
	)

	go func() {
		ps, err := testprog.Wait()
		if err != nil {
			t.Fatal(err)
		}
		waitchan <- ps
	}()

	proc := startDebugger(t, testprog.Pid)

	defer func() {
		testprog.Kill()
		proc.Kill()

		err := os.Remove("dbg-test")
		if err != nil {
			t.Fatal(err)
		}
	}()

	timer := time.NewTimer(5 * time.Second)
	select {
	case ps := <-waitchan:
		stat := ps.Sys().(syscall.WaitStatus)
		if stat.Signaled() && strings.Contains(ps.String(), "exited") {
			t.Fatal("Process has not exited")
		}

	case <-timer.C:
		t.Fatal("timeout")
	}
}
