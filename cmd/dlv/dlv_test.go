package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	protest "github.com/derekparker/delve/pkg/proc/test"
	"github.com/derekparker/delve/service/rpc2"
)

var testBackend string

func TestMain(m *testing.M) {
	flag.StringVar(&testBackend, "backend", "", "selects backend")
	flag.Parse()
	if testBackend == "" {
		testBackend = os.Getenv("PROCTEST")
		if testBackend == "" {
			testBackend = "native"
		}
	}
	os.Exit(m.Run())
}

func assertNoError(err error, t testing.TB, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func goEnv(name string) string {
	if val := os.Getenv(name); val != "" {
		return val
	}
	val, err := exec.Command("go", "env", name).Output()
	if err != nil {
		panic(err) // the Go tool was tested to work earlier
	}
	return strings.TrimSpace(string(val))
}

func TestBuild(t *testing.T) {
	const listenAddr = "localhost:40573"
	var err error
	makedir := filepath.Join(goEnv("GOPATH"), "src", "github.com", "derekparker", "delve")
	for _, makeProgram := range []string{"make", "mingw32-make"} {
		var out []byte
		cmd := exec.Command(makeProgram, "build")
		cmd.Dir = makedir
		out, err = cmd.CombinedOutput()
		if err == nil {
			break
		} else {
			t.Logf("makefile error %s (%s): %v", makeProgram, makedir, err)
			t.Logf("output %s", string(out))
		}
	}
	assertNoError(err, t, "make")
	dlvbin := filepath.Join(makedir, "dlv")
	defer os.Remove(dlvbin)

	fixtures := protest.FindFixturesDir()

	buildtestdir := filepath.Join(fixtures, "buildtest")

	cmd := exec.Command(dlvbin, "debug", "--headless=true", "--listen="+listenAddr, "--api-version=2", "--backend="+testBackend)
	cmd.Dir = buildtestdir
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
	cmd.Start()

	scan := bufio.NewScanner(stdout)
	// wait for the debugger to start
	scan.Scan()
	go func() {
		for scan.Scan() {
			// keep pipe empty
		}
	}()

	client := rpc2.NewClient(listenAddr)
	state := <-client.Continue()

	if !state.Exited {
		t.Fatal("Program did not exit")
	}

	client.Detach(true)
	cmd.Wait()
}

func testIssue398(t *testing.T, dlvbin string, cmds []string) (stdout, stderr []byte) {
	var stdoutBuf, stderrBuf bytes.Buffer
	buildtestdir := filepath.Join(protest.FindFixturesDir(), "buildtest")
	cmd := exec.Command(dlvbin, "debug")
	cmd.Dir = buildtestdir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		fmt.Fprintf(stdin, "%s\n", c)
	}
	// ignore "dlv debug" command error, it returns
	// errors even after successful debug session.
	cmd.Wait()
	stdout, stderr = stdoutBuf.Bytes(), stderrBuf.Bytes()

	debugbin := filepath.Join(buildtestdir, "debug")
	_, err = os.Stat(debugbin)
	if err == nil {
		t.Errorf("running %q: file %v was not deleted\nstdout is %q, stderr is %q", cmds, debugbin, stdout, stderr)
		return
	}
	if !os.IsNotExist(err) {
		t.Errorf("running %q: %v\nstdout is %q, stderr is %q", cmds, err, stdout, stderr)
		return
	}
	return
}

// TestIssue398 verifies that the debug executable is removed after exit.
func TestIssue398(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "TestIssue398")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpdir)

	dlvbin := filepath.Join(tmpdir, "dlv.exe")
	out, err := exec.Command("go", "build", "-o", dlvbin, "github.com/derekparker/delve/cmd/dlv").CombinedOutput()
	if err != nil {
		t.Fatalf("go build -o %v github.com/derekparker/delve/cmd/dlv: %v\n%s", dlvbin, err, string(out))
	}

	testIssue398(t, dlvbin, []string{"exit"})

	const hello = "hello world!"
	stdout, _ := testIssue398(t, dlvbin, []string{"continue", "exit"})
	if !strings.Contains(string(stdout), hello) {
		t.Errorf("stdout %q should contain %q", stdout, hello)
	}
}
