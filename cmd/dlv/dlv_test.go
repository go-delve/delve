package main_test

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

	"github.com/derekparker/delve/cmd/dlv/cmds"
	protest "github.com/derekparker/delve/pkg/proc/test"
	"github.com/derekparker/delve/pkg/terminal"
	"github.com/derekparker/delve/service/rpc2"
	"github.com/spf13/cobra/doc"
)

var testBackend string

func TestMain(m *testing.M) {
	flag.StringVar(&testBackend, "backend", "", "selects backend")
	flag.Parse()
	if testBackend == "" {
		testBackend = os.Getenv("PROCTEST")
		if testBackend == "" {
			testBackend = "native"
			if runtime.GOOS == "darwin" {
				testBackend = "lldb"
			}
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

func projectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	gopaths := strings.FieldsFunc(os.Getenv("GOPATH"), func(r rune) bool { return r == os.PathListSeparator })
	for _, curpath := range gopaths {
		// Detects "gopath mode" when GOPATH contains several paths ex. "d:\\dir\\gopath;f:\\dir\\gopath2"
		if strings.Contains(wd, curpath) {
			return filepath.Join(curpath, "src", "github.com", "derekparker", "delve")
		}
	}
	val, err := exec.Command("go", "list", "-m", "-f", "{{ .Dir }}").Output()
	if err != nil {
		panic(err) // the Go tool was tested to work earlier
	}
	return strings.TrimSuffix(string(val), "\n")
}

func TestBuild(t *testing.T) {
	const listenAddr = "localhost:40573"
	var err error

	cmd := exec.Command("go", "run", "scripts/make.go", "build")
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("makefile error: %v\noutput %s\n", err, string(out))
	}

	dlvbin := filepath.Join(cmd.Dir, "dlv")
	defer os.Remove(dlvbin)

	fixtures := protest.FindFixturesDir()

	buildtestdir := filepath.Join(fixtures, "buildtest")

	cmd = exec.Command(dlvbin, "debug", "--headless=true", "--listen="+listenAddr, "--api-version=2", "--backend="+testBackend, "--log", "--log-output=debugger,rpc")
	cmd.Dir = buildtestdir
	stderr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	cmd.Start()

	scan := bufio.NewScanner(stderr)
	// wait for the debugger to start
	scan.Scan()
	t.Log(scan.Text())
	go func() {
		for scan.Scan() {
			t.Log(scan.Text())
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

func checkAutogenDoc(t *testing.T, filename, gencommand string, generated []byte) {
	saved := slurpFile(t, filepath.Join(projectRoot(), filename))

	if len(saved) != len(generated) {
		t.Fatalf("%s: needs to be regenerated; run %s", filename, gencommand)
	}

	for i := range saved {
		if saved[i] != generated[i] {
			t.Fatalf("%s: needs to be regenerated; run %s", filename, gencommand)
		}
	}
}

func slurpFile(t *testing.T, filename string) []byte {
	saved, err := ioutil.ReadFile(filename)
	if err != nil {
		t.Fatalf("Could not read %s: %v", filename, err)
	}
	return saved
}

// TestGeneratedDoc tests that the autogenerated documentation has been
// updated.
func TestGeneratedDoc(t *testing.T) {
	// Checks gen-cli-docs.go
	var generatedBuf bytes.Buffer
	commands := terminal.DebugCommands(nil)
	commands.WriteMarkdown(&generatedBuf)
	cliDocFilename := "Documentation/cli/README.md"
	checkAutogenDoc(t, cliDocFilename, "scripts/gen-cli-docs.go", generatedBuf.Bytes())

	// Checks gen-usage-docs.go
	tempDir, err := ioutil.TempDir(os.TempDir(), "test-gen-doc")
	assertNoError(err, t, "TempDir")
	defer cmds.SafeRemoveAll(tempDir)
	doc.GenMarkdownTree(cmds.New(true), tempDir)
	entries, err := ioutil.ReadDir(tempDir)
	assertNoError(err, t, "ReadDir")
	for _, doc := range entries {
		docFilename := "Documentation/usage/" + doc.Name()
		checkAutogenDoc(t, docFilename, "scripts/gen-usage-docs.go", slurpFile(t, tempDir+"/"+doc.Name()))
	}
}

func TestExitInInit(t *testing.T) {
	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	buildtestdir := filepath.Join(protest.FindFixturesDir(), "buildtest")
	exitInit := filepath.Join(protest.FindFixturesDir(), "exit.init")
	cmd := exec.Command(dlvbin, "--init", exitInit, "debug")
	cmd.Dir = buildtestdir
	out, err := cmd.CombinedOutput()
	t.Logf("%q %v\n", string(out), err)
	// dlv will exit anyway because stdin is not a tty but it will print the
	// prompt once if the init file didn't call exit successfully.
	if strings.Contains(string(out), "(dlv)") {
		t.Fatal("init did not cause dlv to exit")
	}
}
