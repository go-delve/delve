package main

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	cmd.Stdin = bytes.NewBufferString("exit\ny\n")

	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	return cmd.Process
}

func startTestProg(t *testing.T, proc string) *os.Process {
	cmd := exec.Command(proc)

	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	return cmd.Process
}

func TestCleanExit(t *testing.T) {
	buildBinary(t)

	var (
		waitchan = make(chan *os.ProcessState)
		testprog = startTestProg(t, "_fixtures/livetestprog")
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
		// Admittedly, this is weird.
		// There is/was a bug in Go that marked
		// a process as done whenever `Wait()` was
		// called on it, I fixed that, but it seems
		// there may be another connected bug somewhere
		// where the process is not marked as exited.
		if strings.Contains(ps.String(), "exited") {
			t.Fatal("Process has not exited")
		}

	case <-timer.C:
		t.Fatal("timeout")
	}
}
