package main_test

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/pkg/terminal"
	"github.com/go-delve/delve/service/dap/daptest"
	"github.com/go-delve/delve/service/rpc2"
	"golang.org/x/tools/go/packages"
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
	os.Exit(protest.RunTestsWithFixtures(m))
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
			return filepath.Join(curpath, "src", "github.com", "go-delve", "delve")
		}
	}
	val, err := exec.Command("go", "list", "-mod=", "-m", "-f", "{{ .Dir }}").Output()
	if err != nil {
		panic(err) // the Go tool was tested to work earlier
	}
	return strings.TrimSuffix(string(val), "\n")
}

func TestBuild(t *testing.T) {
	const listenAddr = "127.0.0.1:40573"
	var err error

	cmd := exec.Command("go", "run", "_scripts/make.go", "build")
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
	defer stderr.Close()

	assertNoError(cmd.Start(), t, "dlv debug")

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

	c := []string{dlvbin, "debug", "--allow-non-terminal-interactive=true"}
	debugbin := filepath.Join(buildtestdir, "__debug_bin")
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
	assertNoError(err, t, "stdin pipe")
	defer stdin.Close()

	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	assertNoError(cmd.Start(), t, "dlv debug with output")

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
		// Sometimes delve on Windows can't remove the built binary before
		// exiting and gets an "Access is denied" error when trying.
		// See: https://travis-ci.com/go-delve/delve/jobs/296325131)
		// We have added a delay to gobuild.Remove, but to avoid any test
		// flakiness, we guard against this failure here as well.
		if runtime.GOOS != "windows" || !strings.Contains(err.Error(), "Access is denied") {
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
	out, err := exec.Command("go", "build", "-o", dlvbin, "github.com/go-delve/delve/cmd/dlv").CombinedOutput()
	if err != nil {
		t.Fatalf("go build -o %v github.com/go-delve/delve/cmd/dlv: %v\n%s", dlvbin, err, string(out))
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

// TestContinue verifies that the debugged executable starts immediately with --continue
func TestContinue(t *testing.T) {
	const listenAddr = "127.0.0.1:40573"

	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	buildtestdir := filepath.Join(protest.FindFixturesDir(), "buildtest")
	cmd := exec.Command(dlvbin, "debug", "--headless", "--continue", "--accept-multiclient", "--listen", listenAddr)
	cmd.Dir = buildtestdir
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stderr pipe")
	defer stdout.Close()

	assertNoError(cmd.Start(), t, "start headless instance")

	scan := bufio.NewScanner(stdout)
	// wait for the debugger to start
	for scan.Scan() {
		t.Log(scan.Text())
		if scan.Text() == "hello world!" {
			break
		}
	}

	// and detach from and kill the headless instance
	client := rpc2.NewClient(listenAddr)
	if err := client.Detach(true); err != nil {
		t.Fatalf("error detaching from headless instance: %v", err)
	}
	cmd.Wait()
}

// TestChildProcessExitWhenNoDebugInfo verifies that the child process exits when dlv launch the binary without debug info
func TestChildProcessExitWhenNoDebugInfo(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("test skipped on darwin, see https://github.com/go-delve/delve/pull/2018 for details")
	}

	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("test skipped, `ps` not found")
	}

	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	fix := protest.BuildFixture("http_server", protest.LinkStrip)

	// dlv exec the binary file and expect error.
	if _, err := exec.Command(dlvbin, "exec", fix.Path).CombinedOutput(); err == nil {
		t.Fatalf("Expected err when launching the binary without debug info, but got nil")
	}

	// search the running process named fix.Name
	cmd := exec.Command("ps", "-aux")
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stderr pipe")
	defer stdout.Close()

	assertNoError(cmd.Start(), t, "start `ps -aux`")

	var foundFlag bool
	scan := bufio.NewScanner(stdout)
	for scan.Scan() {
		t.Log(scan.Text())
		if strings.Contains(scan.Text(), fix.Name) {
			foundFlag = true
			break
		}
	}
	cmd.Wait()

	if foundFlag {
		t.Fatalf("Expected child process exited, but found it running")
	}
}

// TestRedirect verifies that redirecting stdin works
func TestRedirect(t *testing.T) {
	const listenAddr = "127.0.0.1:40573"

	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	catfixture := filepath.Join(protest.FindFixturesDir(), "cat.go")
	cmd := exec.Command(dlvbin, "debug", "--headless", "--continue", "--accept-multiclient", "--listen", listenAddr, "-r", catfixture, catfixture)
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stderr pipe")
	defer stdout.Close()

	assertNoError(cmd.Start(), t, "start headless instance")

	scan := bufio.NewScanner(stdout)
	// wait for the debugger to start
	for scan.Scan() {
		t.Log(scan.Text())
		if scan.Text() == "read \"}\"" {
			break
		}
	}

	// and detach from and kill the headless instance
	client := rpc2.NewClient(listenAddr)
	_ = client.Detach(true)
	cmd.Wait()
}

const checkAutogenDocLongOutput = false

func checkAutogenDoc(t *testing.T, filename, gencommand string, generated []byte) {
	saved := slurpFile(t, filepath.Join(projectRoot(), filename))

	saved = bytes.ReplaceAll(saved, []byte("\r\n"), []byte{'\n'})
	generated = bytes.ReplaceAll(generated, []byte("\r\n"), []byte{'\n'})

	if len(saved) != len(generated) {
		if checkAutogenDocLongOutput {
			t.Logf("generated %q saved %q\n", generated, saved)
		}
		t.Fatalf("%s: needs to be regenerated; run %s", filename, gencommand)
	}

	for i := range saved {
		if saved[i] != generated[i] {
			if checkAutogenDocLongOutput {
				t.Logf("generated %q saved %q\n", generated, saved)
			}
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
	if strings.ToLower(os.Getenv("TRAVIS")) == "true" && runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows in CI")
	}
	// Checks gen-cli-docs.go
	var generatedBuf bytes.Buffer
	commands := terminal.DebugCommands(nil)
	commands.WriteMarkdown(&generatedBuf)
	checkAutogenDoc(t, "Documentation/cli/README.md", "_scripts/gen-cli-docs.go", generatedBuf.Bytes())

	// Checks gen-usage-docs.go
	tempDir, err := ioutil.TempDir(os.TempDir(), "test-gen-doc")
	assertNoError(err, t, "TempDir")
	defer protest.SafeRemoveAll(tempDir)
	cmd := exec.Command("go", "run", "_scripts/gen-usage-docs.go", tempDir)
	cmd.Dir = projectRoot()
	err = cmd.Run()
	assertNoError(err, t, "go run _scripts/gen-usage-docs.go")
	entries, err := ioutil.ReadDir(tempDir)
	assertNoError(err, t, "ReadDir")
	for _, doc := range entries {
		docFilename := "Documentation/usage/" + doc.Name()
		checkAutogenDoc(t, docFilename, "_scripts/gen-usage-docs.go", slurpFile(t, tempDir+"/"+doc.Name()))
	}

	runScript := func(args ...string) []byte {
		a := []string{"run"}
		a = append(a, args...)
		cmd := exec.Command("go", a...)
		cmd.Dir = projectRoot()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("could not run script %v: %v (output: %q)", args, err, string(out))
		}
		return out
	}

	checkAutogenDoc(t, "pkg/terminal/starbind/starlark_mapping.go", "'go generate' inside pkg/terminal/starbind", runScript("_scripts/gen-starlark-bindings.go", "go", "-"))
	checkAutogenDoc(t, "Documentation/cli/starlark.md", "'go generate' inside pkg/terminal/starbind", runScript("_scripts/gen-starlark-bindings.go", "doc/dummy", "Documentation/cli/starlark.md"))
	checkAutogenDoc(t, "Documentation/backend_test_health.md", "go run _scripts/gen-backend_test_health.go", runScript("_scripts/gen-backend_test_health.go", "-"))
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

func getMethods(pkg *types.Package, typename string) map[string]*types.Func {
	r := make(map[string]*types.Func)
	mset := types.NewMethodSet(types.NewPointer(pkg.Scope().Lookup(typename).Type()))
	for i := 0; i < mset.Len(); i++ {
		fn := mset.At(i).Obj().(*types.Func)
		r[fn.Name()] = fn
	}
	return r
}

func publicMethodOf(decl ast.Decl, receiver string) *ast.FuncDecl {
	fndecl, isfunc := decl.(*ast.FuncDecl)
	if !isfunc {
		return nil
	}
	if fndecl.Name.Name[0] >= 'a' && fndecl.Name.Name[0] <= 'z' {
		return nil
	}
	if fndecl.Recv == nil || len(fndecl.Recv.List) != 1 {
		return nil
	}
	starexpr, isstar := fndecl.Recv.List[0].Type.(*ast.StarExpr)
	if !isstar {
		return nil
	}
	identexpr, isident := starexpr.X.(*ast.Ident)
	if !isident || identexpr.Name != receiver {
		return nil
	}
	if fndecl.Body == nil {
		return nil
	}
	return fndecl
}

func findCallCall(fndecl *ast.FuncDecl) *ast.CallExpr {
	for _, stmt := range fndecl.Body.List {
		var x ast.Expr = nil

		switch s := stmt.(type) {
		case *ast.AssignStmt:
			if len(s.Rhs) == 1 {
				x = s.Rhs[0]
			}
		case *ast.ReturnStmt:
			if len(s.Results) == 1 {
				x = s.Results[0]
			}
		case *ast.ExprStmt:
			x = s.X
		}

		callx, iscall := x.(*ast.CallExpr)
		if !iscall {
			continue
		}
		fun, issel := callx.Fun.(*ast.SelectorExpr)
		if !issel || fun.Sel.Name != "call" {
			continue
		}
		return callx
	}
	return nil
}

func qf(*types.Package) string {
	return ""
}

func TestTypecheckRPC(t *testing.T) {
	fset := &token.FileSet{}
	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedName | packages.NeedCompiledGoFiles | packages.NeedTypes,
		Fset: fset,
	}
	pkgs, err := packages.Load(cfg, "github.com/go-delve/delve/service/rpc2")
	if err != nil {
		t.Fatal(err)
	}
	var clientAst *ast.File
	var serverMethods map[string]*types.Func
	var info *types.Info
	packages.Visit(pkgs, func(pkg *packages.Package) bool {
		if pkg.PkgPath != "github.com/go-delve/delve/service/rpc2" {
			return true
		}
		t.Logf("package found: %v", pkg.PkgPath)
		serverMethods = getMethods(pkg.Types, "RPCServer")
		info = pkg.TypesInfo
		for i := range pkg.Syntax {
			t.Logf("file %q", pkg.CompiledGoFiles[i])
			if strings.HasSuffix(pkg.CompiledGoFiles[i], string(os.PathSeparator)+"client.go") {
				clientAst = pkg.Syntax[i]
				break
			}
		}
		return true
	}, nil)

	errcount := 0

	for _, decl := range clientAst.Decls {
		fndecl := publicMethodOf(decl, "RPCClient")
		if fndecl == nil {
			continue
		}

		switch fndecl.Name.Name {
		case "Continue", "Rewind":
			// wrappers over continueDir
			continue
		case "SetReturnValuesLoadConfig", "Disconnect":
			// support functions
			continue
		}

		if fndecl.Name.Name == "Continue" || fndecl.Name.Name == "Rewind" || fndecl.Name.Name == "DirectionCongruentContinue" {
			// using continueDir
			continue
		}

		callx := findCallCall(fndecl)

		if callx == nil {
			t.Errorf("%s: could not find RPC call", fset.Position(fndecl.Pos()))
			errcount++
			continue
		}

		if len(callx.Args) != 3 {
			t.Errorf("%s: wrong number of arguments for RPC call", fset.Position(callx.Pos()))
			errcount++
			continue
		}

		arg0, arg0islit := callx.Args[0].(*ast.BasicLit)
		arg1 := callx.Args[1]
		arg2 := callx.Args[2]
		if !arg0islit || arg0.Kind != token.STRING {
			continue
		}
		name, _ := strconv.Unquote(arg0.Value)
		serverMethod := serverMethods[name]
		if serverMethod == nil {
			t.Errorf("%s: could not find RPC method %q", fset.Position(callx.Pos()), name)
			errcount++
			continue
		}

		params := serverMethod.Type().(*types.Signature).Params()

		if a, e := info.TypeOf(arg1), params.At(0).Type(); !types.AssignableTo(a, e) {
			t.Errorf("%s: wrong type of first argument %s, expected %s", fset.Position(callx.Pos()), types.TypeString(a, qf), types.TypeString(e, qf))
			errcount++
			continue
		}

		if !strings.HasSuffix(params.At(1).Type().String(), "/service.RPCCallback") {
			if a, e := info.TypeOf(arg2), params.At(1).Type(); !types.AssignableTo(a, e) {
				t.Errorf("%s: wrong type of second argument %s, expected %s", fset.Position(callx.Pos()), types.TypeString(a, qf), types.TypeString(e, qf))
				errcount++
				continue
			}
		}

		if clit, ok := arg1.(*ast.CompositeLit); ok {
			typ := params.At(0).Type()
			st := typ.Underlying().(*types.Struct)
			if len(clit.Elts) != st.NumFields() && types.TypeString(typ, qf) != "DebuggerCommand" {
				t.Errorf("%s: wrong number of fields in first argument's literal %d, expected %d", fset.Position(callx.Pos()), len(clit.Elts), st.NumFields())
				errcount++
				continue
			}
		}
	}

	if errcount > 0 {
		t.Errorf("%d errors", errcount)
	}
}

// TestDap verifies that a dap server can be started and shut down.
func TestDap(t *testing.T) {
	const listenAddr = "127.0.0.1:40575"

	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	cmd := exec.Command(dlvbin, "dap", "--log-output=dap", "--log", "--listen", listenAddr)
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
	defer stdout.Close()
	stderr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer stderr.Close()

	assertNoError(cmd.Start(), t, "start dap instance")

	scanOut := bufio.NewScanner(stdout)
	scanErr := bufio.NewScanner(stderr)
	// Wait for the debug server to start
	scanOut.Scan()
	listening := "DAP server listening at: " + listenAddr
	if scanOut.Text() != listening {
		cmd.Process.Kill() // release the port
		t.Fatalf("Unexpected stdout:\ngot  %q\nwant %q", scanOut.Text(), listening)
	}
	go func() {
		for scanErr.Scan() {
			t.Log(scanErr.Text())
		}
	}()

	// Connect a client and request shutdown.
	client := daptest.NewClient(listenAddr)
	client.DisconnectRequest()
	client.ExpectDisconnectResponse(t)
	client.ExpectTerminatedEvent(t)
	if _, err := client.ReadMessage(); err != io.EOF {
		t.Errorf("got %q, want \"EOF\"\n", err)
	}
	client.Close()
	cmd.Wait()
}

func TestTrace(t *testing.T) {
	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	expected := []byte("> goroutine(1): main.foo(99, 9801) => (9900)\n")

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(tmpdir, "__debug"), filepath.Join(fixtures, "issue573.go"), "foo")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")

	assertNoError(cmd.Start(), t, "running trace")

	output, err := ioutil.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
	cmd.Wait()
}

func TestTracePid(t *testing.T) {
	if runtime.GOOS == "linux" {
		bs, _ := ioutil.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
		if bs == nil || strings.TrimSpace(string(bs)) != "0" {
			t.Logf("can not run TestAttachDetach: %v\n", bs)
			return
		}
	}

	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	expected := []byte("goroutine(1): main.A() => ()\n")

	// make process run
	fix := protest.BuildFixture("issue2023", 0)
	targetCmd := exec.Command(fix.Path)
	assertNoError(targetCmd.Start(), t, "execute issue2023")

	if targetCmd.Process == nil || targetCmd.Process.Pid == 0 {
		t.Fatal("expected target process runninng")
	}
	defer targetCmd.Process.Kill()

	// dlv attach the process by pid
	cmd := exec.Command(dlvbin, "trace", "-p", strconv.Itoa(targetCmd.Process.Pid), "main.A")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	assertNoError(cmd.Start(), t, "running trace")

	output, err := ioutil.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}

	cmd.Wait()
}

func TestTraceBreakpointExists(t *testing.T) {
	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	fixtures := protest.FindFixturesDir()
	// We always set breakpoints on some runtime functions at startup, so this would return with
	// a breakpoints exists error.
	// TODO: Perhaps we shouldn't be setting these default breakpoints in trace mode, however.
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(tmpdir, "__debug"), filepath.Join(fixtures, "issue573.go"), "runtime.*")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")

	assertNoError(cmd.Start(), t, "running trace")

	defer cmd.Wait()

	output, err := ioutil.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if bytes.Contains(output, []byte("Breakpoint exists")) {
		t.Fatal("Breakpoint exists errors should be ignored")
	}
}

func TestTracePrintStack(t *testing.T) {
	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(tmpdir, "__debug"), "--stack", "2", filepath.Join(fixtures, "issue573.go"), "foo")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")
	assertNoError(cmd.Start(), t, "running trace")

	defer cmd.Wait()

	output, err := ioutil.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, []byte("Stack:")) && !bytes.Contains(output, []byte("main.main")) {
		t.Fatal("stacktrace not printed")
	}
}

func TestDlvTestChdir(t *testing.T) {
	dlvbin, tmpdir := getDlvBin(t)
	defer os.RemoveAll(tmpdir)

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "--allow-non-terminal-interactive=true", "test", filepath.Join(fixtures, "buildtest"), "--", "-test.v")
	cmd.Stdin = strings.NewReader("continue\nexit\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error executing Delve: %v", err)
	}
	t.Logf("output: %q", out)

	p, _ := filepath.Abs(filepath.Join(fixtures, "buildtest"))
	tgt := "current directory: " + p
	if !strings.Contains(string(out), tgt) {
		t.Errorf("output did not contain expected string %q", tgt)
	}
}
