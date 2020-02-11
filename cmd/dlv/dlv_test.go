package main_test

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
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
			// See: https://ci.appveyor.com/project/go-delve/delve/build/1527
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
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start headless instance: %v", err)
	}

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
	checkAutogenDoc(t, "Documentation/cli/README.md", "scripts/gen-cli-docs.go", generatedBuf.Bytes())

	// Checks gen-usage-docs.go
	tempDir, err := ioutil.TempDir(os.TempDir(), "test-gen-doc")
	assertNoError(err, t, "TempDir")
	defer protest.SafeRemoveAll(tempDir)
	cmd := exec.Command("go", "run", "scripts/gen-usage-docs.go", tempDir)
	cmd.Dir = projectRoot()
	cmd.Run()
	entries, err := ioutil.ReadDir(tempDir)
	assertNoError(err, t, "ReadDir")
	for _, doc := range entries {
		docFilename := "Documentation/usage/" + doc.Name()
		checkAutogenDoc(t, docFilename, "scripts/gen-usage-docs.go", slurpFile(t, tempDir+"/"+doc.Name()))
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

	checkAutogenDoc(t, "pkg/terminal/starbind/starlark_mapping.go", "'go generate' inside pkg/terminal/starbind", runScript("scripts/gen-starlark-bindings.go", "go", "-"))
	checkAutogenDoc(t, "Documentation/cli/starlark.md", "'go generate' inside pkg/terminal/starbind", runScript("scripts/gen-starlark-bindings.go", "doc/dummy", "Documentation/cli/starlark.md"))
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
		Mode: packages.LoadSyntax,
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

		if fndecl.Name.Name == "Continue" || fndecl.Name.Name == "Rewind" {
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
