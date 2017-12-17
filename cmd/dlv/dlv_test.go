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
	"time"

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

func goPath(name string) string {
	if val := os.Getenv(name); val != "" {
		// Use first GOPATH entry if there are multiple.
		return filepath.SplitList(val)[0]
	}

	val, err := exec.Command("go", "env", name).Output()
	if err != nil {
		panic(err) // the Go tool was tested to work earlier
	}
	return filepath.SplitList(strings.TrimSpace(string(val)))[0]
}

func TestBuild(t *testing.T) {
	const listenAddr = "localhost:40573"
	var err error
	makedir := filepath.Join(goPath("GOPATH"), "src", "github.com", "derekparker", "delve")
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

func testOutput(t *testing.T, dlvbin, output string, delveCmds []string) (stdout, stderr []byte) {
	var stdoutBuf, stderrBuf bytes.Buffer
	buildtestdir := filepath.Join(protest.FindFixturesDir(), "buildtest")

	c := []string{dlvbin, "debug"}
	debugbin := filepath.Join(buildtestdir, "debug")
	if output != "" {
		c = append(c, "--output", output)
		if filepath.IsAbs(output) {
			debugbin = output
		} else {
			debugbin = filepath.Join(buildtestdir, output)
		}
	}
	cmd := exec.Command(c[0], c[1:]...)
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

	// Give delve some time to compile and write the binary.
	foundIt := false
	for wait := 0; wait < 30; wait++ {
		_, err = os.Stat(debugbin)
		if err == nil {
			foundIt = true
			break
		}

		time.Sleep(1 * time.Second)
	}
	if !foundIt {
		t.Errorf("running %q: file not created: %v", delveCmds, err)
	}

	for _, c := range delveCmds {
		fmt.Fprintf(stdin, "%s\n", c)
	}

	// ignore "dlv debug" command error, it returns
	// errors even after successful debug session.
	cmd.Wait()
	stdout, stderr = stdoutBuf.Bytes(), stderrBuf.Bytes()

	_, err = os.Stat(debugbin)
	if err == nil {
		if strings.ToLower(os.Getenv("APPVEYOR")) != "true" {
			// Sometimes delve on Appveyor can't remove the built binary before
			// exiting and gets an "Access is denied" error when trying.
			// See: https://ci.appveyor.com/project/derekparker/delve/build/1527
			t.Errorf("running %q: file %v was not deleted\nstdout is %q, stderr is %q", delveCmds, debugbin, stdout, stderr)
		}
		return
	}
	if !os.IsNotExist(err) {
		t.Errorf("running %q: %v\nstdout is %q, stderr is %q", delveCmds, err, stdout, stderr)
		return
	}
	return
}

func getDlvBin(t *testing.T) (string, string) {
	tmpdir, err := ioutil.TempDir("", "TestDlv")
	if err != nil {
		t.Fatal(err)
	}

	dlvbin := filepath.Join(tmpdir, "dlv.exe")
	out, err := exec.Command("go", "build", "-o", dlvbin, "github.com/derekparker/delve/cmd/dlv").CombinedOutput()
	if err != nil {
		t.Fatalf("go build -o %v github.com/derekparker/delve/cmd/dlv: %v\n%s", dlvbin, err, string(out))
	}

	return dlvbin, tmpdir
}

// TestOutput verifies that the debug executable is created in the correct path
// and removed after exit.
func TestOutput(t *testing.T) {
	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	for _, output := range []string{"", "myownname", filepath.Join(tmpdir, "absolute.path")} {
		testOutput(t, dlvbin, output, []string{"exit"})

		const hello = "hello world!"
		stdout, _ := testOutput(t, dlvbin, output, []string{"continue", "exit"})
		if !strings.Contains(string(stdout), hello) {
			t.Errorf("stdout %q should contain %q", stdout, hello)
		}
	}
}
