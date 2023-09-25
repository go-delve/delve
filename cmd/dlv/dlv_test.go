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
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/goversion"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/pkg/terminal"
	"github.com/go-delve/delve/service/dap"
	"github.com/go-delve/delve/service/dap/daptest"
	"github.com/go-delve/delve/service/rpc2"
	godap "github.com/google/go-dap"
	"golang.org/x/tools/go/packages"
)

var testBackend string
var ldFlags string

func init() {
	ldFlags = os.Getenv("CGO_LDFLAGS")
}

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
	t.Helper()
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
	for scan.Scan() {
		text := scan.Text()
		t.Log(text)
		if strings.Contains(text, "API server pid = ") {
			break
		}
	}
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

func getDlvBin(t *testing.T) string {
	// In case this was set in the environment
	// from getDlvBinEBPF lets clear it here so
	// we can ensure we don't get build errors
	// depending on the test ordering.
	t.Setenv("CGO_LDFLAGS", ldFlags)
	var tags string
	if runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
		tags = "-tags=exp.winarm64"
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "ppc64le" {
		tags = "-tags=exp.linuxppc64le"
	}
	return getDlvBinInternal(t, tags)
}

func getDlvBinEBPF(t *testing.T) string {
	return getDlvBinInternal(t, "-tags", "ebpf")
}

func getDlvBinInternal(t *testing.T, goflags ...string) string {
	dlvbin := filepath.Join(t.TempDir(), "dlv.exe")
	args := append([]string{"build", "-o", dlvbin}, goflags...)
	args = append(args, "github.com/go-delve/delve/cmd/dlv")

	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("go build -o %v github.com/go-delve/delve/cmd/dlv: %v\n%s", dlvbin, err, string(out))
	}

	return dlvbin
}

// TestOutput verifies that the debug executable is created in the correct path
// and removed after exit.
func TestOutput(t *testing.T) {
	dlvbin := getDlvBin(t)

	for _, output := range []string{"__debug_bin", "myownname", filepath.Join(t.TempDir(), "absolute.path")} {
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

	dlvbin := getDlvBin(t)

	buildtestdir := filepath.Join(protest.FindFixturesDir(), "buildtest")
	cmd := exec.Command(dlvbin, "debug", "--headless", "--continue", "--accept-multiclient", "--listen", listenAddr)
	cmd.Dir = buildtestdir
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
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

// TestRedirect verifies that redirecting stdin works
func TestRedirect(t *testing.T) {
	const listenAddr = "127.0.0.1:40573"

	dlvbin := getDlvBin(t)

	catfixture := filepath.Join(protest.FindFixturesDir(), "cat.go")
	cmd := exec.Command(dlvbin, "debug", "--headless", "--continue", "--accept-multiclient", "--listen", listenAddr, "-r", catfixture, catfixture)
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
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
		diffMaybe(t, filename, generated)
		t.Fatalf("%s: needs to be regenerated; run %s", filename, gencommand)
	}

	for i := range saved {
		if saved[i] != generated[i] {
			if checkAutogenDocLongOutput {
				t.Logf("generated %q saved %q\n", generated, saved)
			}
			diffMaybe(t, filename, generated)
			t.Fatalf("%s: needs to be regenerated; run %s", filename, gencommand)
		}
	}
}

func slurpFile(t *testing.T, filename string) []byte {
	saved, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Could not read %s: %v", filename, err)
	}
	return saved
}

func diffMaybe(t *testing.T, filename string, generated []byte) {
	_, err := exec.LookPath("diff")
	if err != nil {
		return
	}
	cmd := exec.Command("diff", filename, "-")
	cmd.Dir = projectRoot()
	stdin, _ := cmd.StdinPipe()
	go func() {
		stdin.Write(generated)
		stdin.Close()
	}()
	out, _ := cmd.CombinedOutput()
	t.Logf("diff:\n%s", string(out))
}

// TestGeneratedDoc tests that the autogenerated documentation has been
// updated.
func TestGeneratedDoc(t *testing.T) {
	if strings.ToLower(os.Getenv("TRAVIS")) == "true" && runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows in CI")
	}
	if runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
		//TODO(qmuntal): investigate further when the Windows ARM64 backend is more stable.
		t.Skip("skipping test on Windows in CI")
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "ppc64le" {
		//TODO(alexsaezm): finish CI integration
		t.Skip("skipping test on Linux/PPC64LE in CI")
	}
	// Checks gen-cli-docs.go
	var generatedBuf bytes.Buffer
	commands := terminal.DebugCommands(nil)
	commands.WriteMarkdown(&generatedBuf)
	checkAutogenDoc(t, "Documentation/cli/README.md", "_scripts/gen-cli-docs.go", generatedBuf.Bytes())

	// Checks gen-usage-docs.go
	tempDir := t.TempDir()
	cmd := exec.Command("go", "run", "_scripts/gen-usage-docs.go", tempDir)
	cmd.Dir = projectRoot()
	err := cmd.Run()
	assertNoError(err, t, "go run _scripts/gen-usage-docs.go")
	entries, err := os.ReadDir(tempDir)
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

	checkAutogenDoc(t, "Documentation/backend_test_health.md", "go run _scripts/gen-backend_test_health.go", runScript("_scripts/gen-backend_test_health.go", "-"))
	checkAutogenDoc(t, "pkg/terminal/starbind/starlark_mapping.go", "'go generate' inside pkg/terminal/starbind", runScript("_scripts/gen-starlark-bindings.go", "go", "-"))
	checkAutogenDoc(t, "Documentation/cli/starlark.md", "'go generate' inside pkg/terminal/starbind", runScript("_scripts/gen-starlark-bindings.go", "doc/dummy", "Documentation/cli/starlark.md"))
	checkAutogenDoc(t, "Documentation/faq.md", "'go run _scripts/gen-faq-toc.go Documentation/faq.md Documentation/faq.md'", runScript("_scripts/gen-faq-toc.go", "Documentation/faq.md", "-"))
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 18) {
		checkAutogenDoc(t, "_scripts/rtype-out.txt", "go run _scripts/rtype.go report _scripts/rtype-out.txt", runScript("_scripts/rtype.go", "report"))
		runScript("_scripts/rtype.go", "check")
	}
}

func TestExitInInit(t *testing.T) {
	dlvbin := getDlvBin(t)

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

// TestDAPCmd verifies that a dap server can be started and shut down.
func TestDAPCmd(t *testing.T) {
	const listenAddr = "127.0.0.1:40575"

	dlvbin := getDlvBin(t)

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
	_, err = client.ReadMessage()
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Errorf("got %q, want non-nil\n", err)
		}
	} else {
		if err != io.EOF {
			t.Errorf("got %q, want \"EOF\"\n", err)
		}
	}
	client.Close()
	cmd.Wait()
}

func newDAPRemoteClient(t *testing.T, addr string, isDlvAttach bool, isMulti bool) *daptest.Client {
	c := daptest.NewClient(addr)
	c.AttachRequest(map[string]interface{}{"mode": "remote", "stopOnEntry": true})
	if isDlvAttach || isMulti {
		c.ExpectCapabilitiesEventSupportTerminateDebuggee(t)
	}
	c.ExpectInitializedEvent(t)
	c.ExpectAttachResponse(t)
	c.ConfigurationDoneRequest()
	c.ExpectStoppedEvent(t)
	c.ExpectConfigurationDoneResponse(t)
	return c
}

func TestRemoteDAPClient(t *testing.T) {
	const listenAddr = "127.0.0.1:40576"

	dlvbin := getDlvBin(t)

	buildtestdir := filepath.Join(protest.FindFixturesDir(), "buildtest")
	cmd := exec.Command(dlvbin, "debug", "--headless", "--log-output=dap", "--log", "--listen", listenAddr)
	cmd.Dir = buildtestdir
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
	defer stdout.Close()
	stderr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer stderr.Close()
	assertNoError(cmd.Start(), t, "start headless instance")

	scanOut := bufio.NewScanner(stdout)
	scanErr := bufio.NewScanner(stderr)
	// Wait for the debug server to start
	scanOut.Scan()
	t.Log(scanOut.Text())
	go func() { // Capture logging
		for scanErr.Scan() {
			t.Log(scanErr.Text())
		}
	}()

	client := newDAPRemoteClient(t, listenAddr, false, false)
	client.ContinueRequest(1)
	client.ExpectContinueResponse(t)
	client.ExpectTerminatedEvent(t)

	client.DisconnectRequest()
	client.ExpectOutputEventProcessExited(t, 0)
	client.ExpectOutputEventDetaching(t)
	client.ExpectDisconnectResponse(t)
	client.ExpectTerminatedEvent(t)
	if _, err := client.ReadMessage(); err == nil {
		t.Error("expected read error upon shutdown")
	}
	client.Close()
	cmd.Wait()
}

func closeDAPRemoteMultiClient(t *testing.T, c *daptest.Client, expectStatus string) {
	c.DisconnectRequest()
	c.ExpectOutputEventClosingClient(t, expectStatus)
	c.ExpectDisconnectResponse(t)
	c.ExpectTerminatedEvent(t)
	c.Close()
	time.Sleep(10 * time.Millisecond)
}

func TestRemoteDAPClientMulti(t *testing.T) {
	const listenAddr = "127.0.0.1:40577"

	dlvbin := getDlvBin(t)

	buildtestdir := filepath.Join(protest.FindFixturesDir(), "buildtest")
	cmd := exec.Command(dlvbin, "debug", "--headless", "--accept-multiclient", "--log-output=debugger", "--log", "--listen", listenAddr)
	cmd.Dir = buildtestdir
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
	defer stdout.Close()
	stderr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer stderr.Close()
	assertNoError(cmd.Start(), t, "start headless instance")

	scanOut := bufio.NewScanner(stdout)
	scanErr := bufio.NewScanner(stderr)
	// Wait for the debug server to start
	scanOut.Scan()
	t.Log(scanOut.Text())
	go func() { // Capture logging
		for scanErr.Scan() {
			t.Log(scanErr.Text())
		}
	}()

	// Client 0 connects but with the wrong attach request
	dapclient0 := daptest.NewClient(listenAddr)
	dapclient0.AttachRequest(map[string]interface{}{"mode": "local"})
	dapclient0.ExpectErrorResponse(t)

	// Client 1 connects and continues to main.main
	dapclient := newDAPRemoteClient(t, listenAddr, false, true)
	dapclient.SetFunctionBreakpointsRequest([]godap.FunctionBreakpoint{{Name: "main.main"}})
	dapclient.ExpectSetFunctionBreakpointsResponse(t)
	dapclient.ContinueRequest(1)
	dapclient.ExpectContinueResponse(t)
	dapclient.ExpectStoppedEvent(t)
	dapclient.CheckStopLocation(t, 1, "main.main", 5)
	closeDAPRemoteMultiClient(t, dapclient, "halted")

	// Client 2 reconnects at main.main and continues to process exit
	dapclient2 := newDAPRemoteClient(t, listenAddr, false, true)
	dapclient2.CheckStopLocation(t, 1, "main.main", 5)
	dapclient2.ContinueRequest(1)
	dapclient2.ExpectContinueResponse(t)
	dapclient2.ExpectTerminatedEvent(t)
	closeDAPRemoteMultiClient(t, dapclient2, "exited")

	// Attach to exited processes is an error
	dapclient3 := daptest.NewClient(listenAddr)
	dapclient3.AttachRequest(map[string]interface{}{"mode": "remote", "stopOnEntry": true})
	dapclient3.ExpectErrorResponseWith(t, dap.FailedToAttach, `Process \d+ has exited with status 0`, true)
	closeDAPRemoteMultiClient(t, dapclient3, "exited")

	// But rpc clients can still connect and restart
	rpcclient := rpc2.NewClient(listenAddr)
	if _, err := rpcclient.Restart(false); err != nil {
		t.Errorf("error restarting with rpc client: %v", err)
	}
	if err := rpcclient.Detach(true); err != nil {
		t.Fatalf("error detaching from headless instance: %v", err)
	}
	cmd.Wait()
}

func TestRemoteDAPClientAfterContinue(t *testing.T) {
	const listenAddr = "127.0.0.1:40578"

	dlvbin := getDlvBin(t)

	fixture := protest.BuildFixture("loopprog", 0)
	cmd := exec.Command(dlvbin, "exec", fixture.Path, "--headless", "--continue", "--accept-multiclient", "--log-output=debugger,dap", "--log", "--listen", listenAddr)
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "stdout pipe")
	defer stdout.Close()
	stderr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer stderr.Close()
	assertNoError(cmd.Start(), t, "start headless instance")

	scanOut := bufio.NewScanner(stdout)
	scanErr := bufio.NewScanner(stderr)
	// Wait for the debug server to start
	scanOut.Scan() // "API server listening...""
	t.Log(scanOut.Text())
	// Wait for the program to start
	scanOut.Scan() // "past main"
	t.Log(scanOut.Text())

	go func() { // Capture logging
		for scanErr.Scan() {
			text := scanErr.Text()
			if strings.Contains(text, "Internal Error") {
				t.Error("ERROR", text)
			} else {
				t.Log(text)
			}
		}
	}()

	c := newDAPRemoteClient(t, listenAddr, false, true)
	c.ContinueRequest(1)
	c.ExpectContinueResponse(t)
	c.DisconnectRequest()
	c.ExpectOutputEventClosingClient(t, "running")
	c.ExpectDisconnectResponse(t)
	c.ExpectTerminatedEvent(t)
	c.Close()

	c = newDAPRemoteClient(t, listenAddr, false, true)
	c.DisconnectRequestWithKillOption(true)
	c.ExpectOutputEventDetachingKill(t)
	c.ExpectDisconnectResponse(t)
	c.ExpectTerminatedEvent(t)
	if _, err := c.ReadMessage(); err == nil {
		t.Error("expected read error upon shutdown")
	}
	c.Close()
	cmd.Wait()
}

// TestDAPCmdWithClient tests dlv dap --client-addr can be started and shut down.
func TestDAPCmdWithClient(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("cannot setup listener required for testing: %v", err)
	}
	defer listener.Close()

	dlvbin := getDlvBin(t)

	cmd := exec.Command(dlvbin, "dap", "--log-output=dap", "--log", "--client-addr", listener.Addr().String())
	buf := &bytes.Buffer{}
	cmd.Stdin = buf
	cmd.Stdout = buf
	assertNoError(cmd.Start(), t, "start dlv dap process with --client-addr flag")

	// Wait for the connection.
	conn, err := listener.Accept()
	if err != nil {
		cmd.Process.Kill() // release the port
		t.Fatalf("Failed to get connection: %v", err)
	}
	t.Log("dlv dap process dialed in successfully")

	client := daptest.NewClientFromConn(conn)
	client.InitializeRequest()
	client.ExpectInitializeResponse(t)

	// Close the connection.
	if err := conn.Close(); err != nil {
		cmd.Process.Kill()
		t.Fatalf("Failed to get connection: %v", err)
	}

	// Connection close should trigger dlv-reverse command's normal exit.
	if err := cmd.Wait(); err != nil {
		cmd.Process.Kill()
		t.Fatalf("command failed: %v\n%s\n%v", err, buf.Bytes(), cmd.Process.Pid)
	}
}

func TestTrace(t *testing.T) {
	dlvbin := getDlvBin(t)

	expected := []byte("> goroutine(1): main.foo(99, 9801)\n>> goroutine(1): => (9900)\n")

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "issue573.go"), "foo")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
	cmd.Wait()
}

func TestTrace2(t *testing.T) {
	dlvbin := getDlvBin(t)

	expected := []byte("> goroutine(1): main.callme(2)\n>> goroutine(1): => (4)\n")

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "traceprog.go"), "callme")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
	assertNoError(cmd.Wait(), t, "cmd.Wait()")
}

func TestTraceMultipleGoroutines(t *testing.T) {
	dlvbin := getDlvBin(t)

	// TODO(derekparker) this test has to be a bit vague to avoid flakyness.
	// I think a future improvement could be to use regexp captures to match the
	// goroutine IDs at function entry and exit.
	expected := []byte("main.callme(0, \"five\")\n")
	expected2 := []byte("=> (0)\n")

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "goroutines-trace.go"), "callme")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
	if !bytes.Contains(output, expected2) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
	cmd.Wait()
}

func TestTracePid(t *testing.T) {
	if runtime.GOOS == "linux" {
		bs, _ := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
		if bs == nil || strings.TrimSpace(string(bs)) != "0" {
			t.Logf("can not run TestAttachDetach: %v\n", bs)
			return
		}
	}

	dlvbin := getDlvBin(t)

	expected := []byte("goroutine(1): main.A()\n>> goroutine(1): => ()\n")

	// make process run
	fix := protest.BuildFixture("issue2023", 0)
	targetCmd := exec.Command(fix.Path)
	assertNoError(targetCmd.Start(), t, "execute issue2023")

	if targetCmd.Process == nil || targetCmd.Process.Pid == 0 {
		t.Fatal("expected target process running")
	}
	defer targetCmd.Process.Kill()

	// dlv attach the process by pid
	cmd := exec.Command(dlvbin, "trace", "-p", strconv.Itoa(targetCmd.Process.Pid), "main.A")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}

	cmd.Wait()
}

func TestTraceBreakpointExists(t *testing.T) {
	dlvbin := getDlvBin(t)

	fixtures := protest.FindFixturesDir()
	// We always set breakpoints on some runtime functions at startup, so this would return with
	// a breakpoints exists error.
	// TODO: Perhaps we shouldn't be setting these default breakpoints in trace mode, however.
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "issue573.go"), "runtime.*")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")

	assertNoError(cmd.Start(), t, "running trace")

	defer cmd.Wait()

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if bytes.Contains(output, []byte("Breakpoint exists")) {
		t.Fatal("Breakpoint exists errors should be ignored")
	}
}

func TestTracePrintStack(t *testing.T) {
	dlvbin := getDlvBin(t)

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--output", filepath.Join(t.TempDir(), "__debug"), "--stack", "2", filepath.Join(fixtures, "issue573.go"), "foo")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	cmd.Dir = filepath.Join(fixtures, "buildtest")
	assertNoError(cmd.Start(), t, "running trace")

	defer cmd.Wait()

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	if !bytes.Contains(output, []byte("Stack:")) && !bytes.Contains(output, []byte("main.main")) {
		t.Fatal("stacktrace not printed")
	}
}

func TestTraceEBPF(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("cannot run test in CI, requires kernel compiled with btf support")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("not implemented on non linux/amd64 systems")
	}
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 16) {
		t.Skip("requires at least Go 1.16 to run test")
	}
	usr, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if usr.Uid != "0" {
		t.Skip("test must be run as root")
	}

	dlvbin := getDlvBinEBPF(t)

	expected := []byte("> (1) main.foo(99, 9801)\n=> \"9900\"")

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--ebpf", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "issue573.go"), "foo")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	cmd.Wait()
	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
}

func TestTraceEBPF2(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("cannot run test in CI, requires kernel compiled with btf support")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("not implemented on non linux/amd64 systems")
	}
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 16) {
		t.Skip("requires at least Go 1.16 to run test")
	}
	usr, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if usr.Uid != "0" {
		t.Skip("test must be run as root")
	}

	dlvbin := getDlvBinEBPF(t)

	expected := []byte(`> (1) main.callme(10)
> (1) main.callme(9)
> (1) main.callme(8)
> (1) main.callme(7)
> (1) main.callme(6)
> (1) main.callme(5)
> (1) main.callme(4)
> (1) main.callme(3)
> (1) main.callme(2)
> (1) main.callme(1)
> (1) main.callme(0)
=> "100"
=> "100"
=> "100"
=> "100"
=> "100"
=> "100"
=> "100"
=> "100"
=> "100"
=> "100"
=> "100"`)

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--ebpf", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "ebpf_trace.go"), "main.callme")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	cmd.Wait()
	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
}

func TestTraceEBPF3(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("cannot run test in CI, requires kernel compiled with btf support")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("not implemented on non linux/amd64 systems")
	}
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 16) {
		t.Skip("requires at least Go 1.16 to run test")
	}
	usr, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if usr.Uid != "0" {
		t.Skip("test must be run as root")
	}

	dlvbin := getDlvBinEBPF(t)

	expected := []byte(`> (1) main.tracedFunction(0)
> (1) main.tracedFunction(1)
> (1) main.tracedFunction(2)
> (1) main.tracedFunction(3)
> (1) main.tracedFunction(4)
> (1) main.tracedFunction(5)
> (1) main.tracedFunction(6)
> (1) main.tracedFunction(7)
> (1) main.tracedFunction(8)
> (1) main.tracedFunction(9)`)

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--ebpf", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "ebpf_trace2.go"), "main.traced")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	cmd.Wait()
	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
}

func TestTraceEBPF4(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("cannot run test in CI, requires kernel compiled with btf support")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("not implemented on non linux/amd64 systems")
	}
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 16) {
		t.Skip("requires at least Go 1.16 to run test")
	}
	usr, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if usr.Uid != "0" {
		t.Skip("test must be run as root")
	}

	dlvbin := getDlvBinEBPF(t)

	expected := []byte(`> (1) main.tracedFunction(0, true, 97)
> (1) main.tracedFunction(1, false, 98)
> (1) main.tracedFunction(2, false, 99)
> (1) main.tracedFunction(3, false, 100)
> (1) main.tracedFunction(4, false, 101)
> (1) main.tracedFunction(5, true, 102)
> (1) main.tracedFunction(6, false, 103)
> (1) main.tracedFunction(7, false, 104)
> (1) main.tracedFunction(8, false, 105)
> (1) main.tracedFunction(9, false, 106)`)

	fixtures := protest.FindFixturesDir()
	cmd := exec.Command(dlvbin, "trace", "--ebpf", "--output", filepath.Join(t.TempDir(), "__debug"), filepath.Join(fixtures, "ebpf_trace3.go"), "main.traced")
	rdr, err := cmd.StderrPipe()
	assertNoError(err, t, "stderr pipe")
	defer rdr.Close()

	assertNoError(cmd.Start(), t, "running trace")

	output, err := io.ReadAll(rdr)
	assertNoError(err, t, "ReadAll")

	cmd.Wait()
	if !bytes.Contains(output, expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", string(expected), string(output))
	}
}

func TestDlvTestChdir(t *testing.T) {
	dlvbin := getDlvBin(t)

	fixtures := protest.FindFixturesDir()

	dotest := func(testargs []string) {
		t.Helper()

		args := []string{"--allow-non-terminal-interactive=true", "test"}
		args = append(args, testargs...)
		args = append(args, "--", "-test.v")
		t.Logf("dlv test %s", args)
		cmd := exec.Command(dlvbin, args...)
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

	dotest([]string{filepath.Join(fixtures, "buildtest")})
	files, _ := filepath.Glob(filepath.Join(fixtures, "buildtest", "*.go"))
	dotest(files)
}

func TestVersion(t *testing.T) {
	dlvbin := getDlvBin(t)

	got, err := exec.Command(dlvbin, "version", "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("error executing `dlv version`: %v\n%s\n", err, got)
	}
	want1 := []byte("mod\tgithub.com/go-delve/delve")
	want2 := []byte("dep\tgithub.com/google/go-dap")
	if !bytes.Contains(got, want1) || !bytes.Contains(got, want2) {
		t.Errorf("got %s\nwant %v and %v in the output", got, want1, want2)
	}
}

func TestStaticcheck(t *testing.T) {
	_, err := exec.LookPath("staticcheck")
	if err != nil {
		t.Skip("staticcheck not installed")
	}
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 22) {
		t.Skip("broken with staticcheck@2023.1")
	}
	// default checks minus SA1019 which complains about deprecated identifiers, which change between versions of Go.
	args := []string{"-tests=false", "-checks=all,-SA1019,-ST1000,-ST1003,-ST1016,-S1021,-ST1023", "github.com/go-delve/delve/..."}
	// * SA1019 is disabled because new deprecations get added on every version
	//   of Go making the output of staticcheck inconsistent depending on the
	//   version of Go used to run it.
	// * ST1000,ST1003,ST1016 are disabled in the default
	//   staticcheck configuration
	// * S1021 "Merge variable declaration and assignment" is disabled because
	//   where we don't do this it is a deliberate style choice.
	// * ST1023 "Redundant type in variable declaration" same as S1021.
	cmd := exec.Command("staticcheck", args...)
	cmd.Dir = projectRoot()
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	out, _ := cmd.CombinedOutput()
	checkAutogenDoc(t, "_scripts/staticcheck-out.txt", fmt.Sprintf("staticcheck %s > _scripts/staticcheck-out.txt", strings.Join(args, " ")), out)
}

func TestDefaultBinary(t *testing.T) {
	// Check that when delve is run twice in the same directory simultaneously
	// it will pick different default output binary paths.
	dlvbin := getDlvBin(t)
	fixture := filepath.Join(protest.FindFixturesDir(), "testargs.go")

	startOne := func() (io.WriteCloser, func() error, *bytes.Buffer) {
		cmd := exec.Command(dlvbin, "debug", "--allow-non-terminal-interactive=true", fixture, "--", "test")
		stdin, _ := cmd.StdinPipe()
		stdoutBuf := new(bytes.Buffer)
		cmd.Stdout = stdoutBuf

		assertNoError(cmd.Start(), t, "dlv debug")
		return stdin, cmd.Wait, stdoutBuf
	}

	stdin1, wait1, stdoutBuf1 := startOne()
	defer stdin1.Close()

	stdin2, wait2, stdoutBuf2 := startOne()
	defer stdin2.Close()

	fmt.Fprintf(stdin1, "continue\nquit\n")
	fmt.Fprintf(stdin2, "continue\nquit\n")

	wait1()
	wait2()

	out1, out2 := stdoutBuf1.String(), stdoutBuf2.String()
	t.Logf("%q", out1)
	t.Logf("%q", out2)
	if out1 == out2 {
		t.Errorf("outputs match")
	}
}
