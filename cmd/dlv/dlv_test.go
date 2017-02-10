package main

import (
	"bufio"
	"flag"
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
	makefilepath := filepath.Join(goEnv("GOPATH"), "src", "github.com", "derekparker", "delve", "Makefile")
	t.Logf("makefile: %q", makefilepath)
	var err error
	for _, make := range []string{"make", "mingw32-make"} {
		err = exec.Command(make, "-f", makefilepath, "build").Run()
		if err == nil {
			break
		}
	}
	assertNoError(err, t, "make")
	wd, _ := os.Getwd()
	dlvbin := filepath.Join(wd, "dlv")
	defer os.Remove(dlvbin)

	fixtures := protest.FindFixturesDir()

	buildtestdir := filepath.Join(fixtures, "buildtest")

	cmd := exec.Command(dlvbin, "debug", "--headless=true", "--listen="+listenAddr, "--api-version=2", "--backend="+testBackend)
	cmd.Dir = buildtestdir
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
	cmd.Start()
	defer func() {
		if runtime.GOOS != "windows" {
			cmd.Process.Signal(os.Interrupt)
			cmd.Wait()
		} else {
			// sending os.Interrupt on windows is not supported
			cmd.Process.Kill()
		}
	}()

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
}
