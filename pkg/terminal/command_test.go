package terminal

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/go-delve/delve/service/rpccommon"
)

var testBackend, buildMode string

func TestMain(m *testing.M) {
	flag.StringVar(&testBackend, "backend", "", "selects backend")
	flag.StringVar(&buildMode, "test-buildmode", "", "selects build mode")
	var logConf string
	flag.StringVar(&logConf, "log", "", "configures logging")
	flag.Parse()
	test.DefaultTestBackend(&testBackend)
	if buildMode != "" && buildMode != "pie" {
		fmt.Fprintf(os.Stderr, "unknown build mode %q", buildMode)
		os.Exit(1)
	}
	logflags.Setup(logConf != "", logConf, "")
	os.Exit(test.RunTestsWithFixtures(m))
}

type FakeTerminal struct {
	*Term
	t testing.TB
}

const logCommandOutput = false

func (ft *FakeTerminal) Exec(cmdstr string) (outstr string, err error) {
	var buf bytes.Buffer
	ft.Term.stdout.pw.w = &buf
	ft.Term.starlarkEnv.Redirect(ft.Term.stdout)
	err = ft.cmds.Call(cmdstr, ft.Term)
	outstr = buf.String()
	if logCommandOutput {
		ft.t.Logf("command %q -> %q", cmdstr, outstr)
	}
	ft.Term.stdout.Flush()
	return
}

func (ft *FakeTerminal) ExecStarlark(starlarkProgram string) (outstr string, err error) {
	var buf bytes.Buffer
	ft.Term.stdout.pw.w = &buf
	ft.Term.starlarkEnv.Redirect(ft.Term.stdout)
	_, err = ft.Term.starlarkEnv.Execute("<stdin>", starlarkProgram, "main", nil)
	outstr = buf.String()
	if logCommandOutput {
		ft.t.Logf("command %q -> %q", starlarkProgram, outstr)
	}
	ft.Term.stdout.Flush()
	return
}

func (ft *FakeTerminal) MustExec(cmdstr string) string {
	ft.t.Helper()
	outstr, err := ft.Exec(cmdstr)
	if err != nil {
		ft.t.Errorf("output of %q: %q", cmdstr, outstr)
		ft.t.Fatalf("Error executing <%s>: %v", cmdstr, err)
	}
	return outstr
}

func (ft *FakeTerminal) MustExecStarlark(starlarkProgram string) string {
	outstr, err := ft.ExecStarlark(starlarkProgram)
	if err != nil {
		ft.t.Errorf("output of %q: %q", starlarkProgram, outstr)
		ft.t.Fatalf("Error executing <%s>: %v", starlarkProgram, err)
	}
	return outstr
}

func (ft *FakeTerminal) AssertExec(cmdstr, tgt string) {
	out := ft.MustExec(cmdstr)
	if out != tgt {
		ft.t.Fatalf("Error executing %q, expected %q got %q", cmdstr, tgt, out)
	}
}

func (ft *FakeTerminal) AssertExecError(cmdstr, tgterr string) {
	_, err := ft.Exec(cmdstr)
	if err == nil {
		ft.t.Fatalf("Expected error executing %q", cmdstr)
	}
	if err.Error() != tgterr {
		ft.t.Fatalf("Expected error %q executing %q, got error %q", tgterr, cmdstr, err.Error())
	}
}

func withTestTerminal(name string, t testing.TB, fn func(*FakeTerminal)) {
	withTestTerminalBuildFlags(name, t, 0, fn)
}

func withTestTerminalBuildFlags(name string, t testing.TB, buildFlags test.BuildFlags, fn func(*FakeTerminal)) {
	if testBackend == "rr" {
		test.MustHaveRecordingAllowed(t)
	}
	t.Setenv("TERM", "dumb")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	if buildMode == "pie" {
		buildFlags |= test.BuildModePIE
	}
	server := rpccommon.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{test.BuildFixture(name, buildFlags).Path},
		Debugger: debugger.Config{
			Backend: testBackend,
		},
	})
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	client := rpc2.NewClient(listener.Addr().String())
	defer func() {
		client.Detach(true)
	}()

	ft := &FakeTerminal{
		t:    t,
		Term: New(client, &config.Config{}),
	}
	fn(ft)
}

func TestCommandDefault(t *testing.T) {
	var (
		cmds = Commands{}
		cmd  = cmds.Find("non-existent-command", noPrefix).cmdFn
	)

	err := cmd(nil, callContext{}, "")
	if err == nil {
		t.Fatal("cmd() did not default")
	}

	if err.Error() != "command not available" {
		t.Fatal("wrong command output")
	}
}

func TestCommandReplayWithoutPreviousCommand(t *testing.T) {
	var (
		cmds = DebugCommands(nil)
		cmd  = cmds.Find("", noPrefix).cmdFn
		err  = cmd(nil, callContext{}, "")
	)

	if err != nil {
		t.Error("Null command not returned", err)
	}
}

func TestCommandThread(t *testing.T) {
	var (
		cmds = DebugCommands(nil)
		cmd  = cmds.Find("thread", noPrefix).cmdFn
	)

	err := cmd(nil, callContext{}, "")
	if err == nil {
		t.Fatal("thread terminal command did not default")
	}

	if err.Error() != "you must specify a thread" {
		t.Fatal("wrong command output: ", err.Error())
	}
}

func TestExecuteFile(t *testing.T) {
	breakCount := 0
	traceCount := 0
	c := &Commands{
		client: nil,
		cmds: []command{
			{aliases: []string{"trace"}, cmdFn: func(t *Term, ctx callContext, args string) error {
				traceCount++
				return nil
			}},
			{aliases: []string{"break"}, cmdFn: func(t *Term, ctx callContext, args string) error {
				breakCount++
				return nil
			}},
		},
	}

	fixturesDir := test.FindFixturesDir()
	err := c.executeFile(nil, filepath.Join(fixturesDir, "bpfile"))
	if err != nil {
		t.Fatalf("executeFile: %v", err)
	}

	if breakCount != 1 || traceCount != 1 {
		t.Fatalf("Wrong counts break: %d trace: %d\n", breakCount, traceCount)
	}
}

func TestIssue354(t *testing.T) {
	printStack(&Term{}, os.Stdout, []api.Stackframe{}, "", false)
	printStack(&Term{}, os.Stdout, []api.Stackframe{
		{Location: api.Location{PC: 0, File: "irrelevant.go", Line: 10, Function: nil},
			Bottom: true}}, "", false)
}

func TestIssue411(t *testing.T) {
	test.AllowRecording(t)
	withTestTerminal("math", t, func(term *FakeTerminal) {
		term.MustExec("break _fixtures/math.go:8")
		term.MustExec("trace _fixtures/math.go:9")
		term.MustExec("continue")
		out := term.MustExec("next")
		if !strings.HasPrefix(out, "> goroutine(1): main.main()") {
			t.Fatalf("Wrong output for next: <%s>", out)
		}
	})
}

func TestTrace(t *testing.T) {
	test.AllowRecording(t)
	withTestTerminal("issue573", t, func(term *FakeTerminal) {
		term.MustExec("trace foo")
		out, _ := term.Exec("continue")
		// The output here is a little strange, but we don't filter stdout vs stderr so it gets jumbled.
		// Therefore we assert about the call and return values separately.
		if !strings.Contains(out, "> goroutine(1): main.foo(99, 9801)") {
			t.Fatalf("Wrong output for tracepoint: %s", out)
		}
		if !strings.Contains(out, "=> (9900)") {
			t.Fatalf("Wrong output for tracepoint return value: %s", out)
		}
	})
}

func TestTraceWithName(t *testing.T) {
	test.AllowRecording(t)
	withTestTerminal("issue573", t, func(term *FakeTerminal) {
		term.MustExec("trace foobar foo")
		out, _ := term.Exec("continue")
		// The output here is a little strange, but we don't filter stdout vs stderr so it gets jumbled.
		// Therefore we assert about the call and return values separately.
		if !strings.Contains(out, "> goroutine(1): [foobar] main.foo(99, 9801)") {
			t.Fatalf("Wrong output for tracepoint: %s", out)
		}
		if !strings.Contains(out, "=> (9900)") {
			t.Fatalf("Wrong output for tracepoint return value: %s", out)
		}
	})
}

func TestTraceOnNonFunctionEntry(t *testing.T) {
	test.AllowRecording(t)
	withTestTerminal("issue573", t, func(term *FakeTerminal) {
		term.MustExec("trace foobar issue573.go:19")
		out, _ := term.Exec("continue")
		if !strings.Contains(out, "> goroutine(1): [foobar] main.foo(99, 9801)") {
			t.Fatalf("Wrong output for tracepoint: %s", out)
		}
		if strings.Contains(out, "=> (9900)") {
			t.Fatalf("Tracepoint on non-function locspec should not have return value:\n%s", out)
		}
	})
}

func TestExitStatus(t *testing.T) {
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		term.Exec("continue")
		status, err := term.handleExit()
		if err != nil {
			t.Fatal(err)
		}
		if status != 0 {
			t.Fatalf("incorrect exit status, expected 0, got %d", status)
		}
	})
}

func TestScopePrefix(t *testing.T) {
	if runtime.GOARCH == "ppc64le" && buildMode == "pie" {
		t.Skip("pie mode broken on ppc64le")
	}
	const goroutinesLinePrefix = "  Goroutine "
	const goroutinesCurLinePrefix = "* Goroutine "
	test.AllowRecording(t)

	lenient := 0
	if runtime.GOOS == "windows" {
		lenient = 1
	}

	withTestTerminal("goroutinestackprog", t, func(term *FakeTerminal) {
		term.MustExec("b stacktraceme")
		term.MustExec("continue")

		goroutinesOut := strings.Split(term.MustExec("goroutines"), "\n")
		agoroutines := []int{}
		nonagoroutines := []int{}
		curgid := -1

		for _, line := range goroutinesOut {
			iscur := strings.HasPrefix(line, goroutinesCurLinePrefix)
			if !iscur && !strings.HasPrefix(line, goroutinesLinePrefix) {
				continue
			}

			dash := strings.Index(line, " - ")
			if dash < 0 {
				continue
			}

			gid, err := strconv.Atoi(line[len(goroutinesLinePrefix):dash])
			if err != nil {
				continue
			}

			if iscur {
				curgid = gid
			}

			if idx := strings.Index(line, " main.agoroutine "); idx < 0 {
				nonagoroutines = append(nonagoroutines, gid)
				continue
			}

			agoroutines = append(agoroutines, gid)
		}

		if len(agoroutines) > 10 {
			t.Fatalf("Output of goroutines did not have 10 goroutines stopped on main.agoroutine (%d found): %q", len(agoroutines), goroutinesOut)
		}

		if len(agoroutines) < 10 {
			extraAgoroutines := 0
			for _, gid := range nonagoroutines {
				stackOut := strings.Split(term.MustExec(fmt.Sprintf("goroutine %d stack", gid)), "\n")
				for _, line := range stackOut {
					if strings.HasSuffix(line, " main.agoroutine") {
						extraAgoroutines++
						break
					}
				}
			}
			if len(agoroutines)+extraAgoroutines < 10-lenient {
				t.Fatalf("Output of goroutines did not have 10 goroutines stopped on main.agoroutine (%d+%d found): %q", len(agoroutines), extraAgoroutines, goroutinesOut)
			}
		}

		if curgid < 0 {
			t.Fatalf("Could not find current goroutine in output of goroutines: %q", goroutinesOut)
		}

		seen := make([]bool, 10)
		for _, gid := range agoroutines {
			stackOut := strings.Split(term.MustExec(fmt.Sprintf("goroutine %d stack", gid)), "\n")
			fid := -1
			for _, line := range stackOut {
				line = strings.TrimLeft(line, " ")
				space := strings.Index(line, " ")
				if space < 0 {
					continue
				}
				curfid, err := strconv.Atoi(line[:space])
				if err != nil {
					continue
				}

				if idx := strings.Index(line, " main.agoroutine"); idx >= 0 {
					fid = curfid
					break
				}
			}
			if fid < 0 {
				t.Fatalf("Could not find frame for goroutine %d: %q", gid, stackOut)
			}
			term.AssertExec(fmt.Sprintf("goroutine     %d    frame     %d     locals", gid, fid), "(no locals)\n")
			argsOut := strings.Split(term.MustExec(fmt.Sprintf("goroutine %d frame %d args", gid, fid)), "\n")
			if len(argsOut) != 4 || argsOut[3] != "" {
				t.Fatalf("Wrong number of arguments in goroutine %d frame %d: %v", gid, fid, argsOut)
			}
			out := term.MustExec(fmt.Sprintf("goroutine %d frame %d p i", gid, fid))
			ival, err := strconv.Atoi(out[:len(out)-1])
			if err != nil {
				t.Fatalf("could not parse value %q of i for goroutine %d frame %d: %v", out, gid, fid, err)
			}
			seen[ival] = true
		}

		for i := range seen {
			if !seen[i] {
				if lenient > 0 {
					lenient--
				} else {
					t.Fatalf("goroutine %d not found", i)
				}
			}
		}

		term.MustExec("c")

		term.AssertExecError("frame", "not enough arguments")
		term.AssertExecError(fmt.Sprintf("goroutine %d frame 10 locals", curgid), fmt.Sprintf("Frame 10 does not exist in goroutine %d", curgid))
		term.AssertExecError("goroutine 9000 locals", "unknown goroutine 9000")

		term.AssertExecError("print n", "could not find symbol value for n")
		term.AssertExec("frame 1 print n", "3\n")
		term.AssertExec("frame 2 print n", "2\n")
		term.AssertExec("frame 3 print n", "1\n")
		term.AssertExec("frame 4 print n", "0\n")
		term.AssertExecError("frame 5 print n", "could not find symbol value for n")

		term.MustExec("frame 2")
		term.AssertExec("print n", "2\n")
		term.MustExec("frame 4")
		term.AssertExec("print n", "0\n")
		term.MustExec("down")
		term.AssertExec("print n", "1\n")
		term.MustExec("down 2")
		term.AssertExec("print n", "3\n")
		term.AssertExecError("down 2", "Invalid frame -1")
		term.AssertExec("print n", "3\n")
		term.MustExec("up 2")
		term.AssertExec("print n", "1\n")
		term.AssertExecError("up 100", "Invalid frame 103")
		term.AssertExec("print n", "1\n")

		term.MustExec("step")
		term.AssertExecError("print n", "could not find symbol value for n")
		term.MustExec("frame 2")
		term.AssertExec("print n", "2\n")
	})
}

func TestOnPrefix(t *testing.T) {
	const prefix = "\ti: "
	test.AllowRecording(t)
	lenient := false
	if runtime.GOOS == "windows" {
		lenient = true
	}
	withTestTerminal("goroutinestackprog", t, func(term *FakeTerminal) {
		term.MustExec("b agobp main.agoroutine")
		term.MustExec("on agobp print i")

		seen := make([]bool, 10)

		for {
			outstr, err := term.Exec("continue")
			if err != nil {
				if !strings.Contains(err.Error(), "exited") {
					t.Fatalf("Unexpected error executing 'continue': %v", err)
				}
				break
			}
			out := strings.Split(outstr, "\n")

			for i := range out {
				if !strings.HasPrefix(out[i], prefix) {
					continue
				}
				id, err := strconv.Atoi(out[i][len(prefix):])
				if err != nil {
					continue
				}
				if seen[id] {
					t.Fatalf("Goroutine %d seen twice\n", id)
				}
				seen[id] = true
			}
		}

		for i := range seen {
			if !seen[i] {
				if lenient {
					lenient = false
				} else {
					t.Fatalf("Goroutine %d not seen\n", i)
				}
			}
		}
	})
}

func TestNoVars(t *testing.T) {
	test.AllowRecording(t)
	withTestTerminal("locationsUpperCase", t, func(term *FakeTerminal) {
		term.MustExec("b main.main")
		term.MustExec("continue")
		term.AssertExec("args", "(no args)\n")
		term.AssertExec("locals", "(no locals)\n")
		term.AssertExec("vars filterThatMatchesNothing", "(no vars)\n")
	})
}

func TestOnPrefixLocals(t *testing.T) {
	const prefix = "\ti: "
	test.AllowRecording(t)
	withTestTerminal("goroutinestackprog", t, func(term *FakeTerminal) {
		term.MustExec("b agobp main.agoroutine")
		term.MustExec("on agobp args -v")

		seen := make([]bool, 10)

		for {
			outstr, err := term.Exec("continue")
			if err != nil {
				if !strings.Contains(err.Error(), "exited") {
					t.Fatalf("Unexpected error executing 'continue': %v", err)
				}
				break
			}
			out := strings.Split(outstr, "\n")

			for i := range out {
				if !strings.HasPrefix(out[i], prefix) {
					continue
				}
				id, err := strconv.Atoi(out[i][len(prefix):])
				if err != nil {
					continue
				}
				if seen[id] {
					t.Fatalf("Goroutine %d seen twice\n", id)
				}
				seen[id] = true
			}
		}

		for i := range seen {
			if !seen[i] {
				t.Fatalf("Goroutine %d not seen\n", i)
			}
		}
	})
}

func listIsAt(t *testing.T, term *FakeTerminal, listcmd string, cur, start, end int) {
	t.Helper()
	outstr := term.MustExec(listcmd)
	lines := strings.Split(outstr, "\n")

	t.Logf("%q: %q", listcmd, outstr)

	if cur >= 0 && !strings.Contains(lines[0], fmt.Sprintf(":%d", cur)) {
		t.Fatalf("Could not find current line number in first output line: %q", lines[0])
	}

	re := regexp.MustCompile(`(=>)?\s+(\d+):`)

	outStart, outEnd := 0, 0

	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		v := re.FindStringSubmatch(line)
		if len(v) != 3 {
			continue
		}
		curline, _ := strconv.Atoi(v[2])
		if v[1] == "=>" {
			if cur != curline {
				t.Fatalf("Wrong current line, got %d expected %d", curline, cur)
			}
		}
		if outStart == 0 {
			outStart = curline
		}
		outEnd = curline
	}

	if start != -1 || end != -1 {
		if outStart != start || outEnd != end {
			t.Fatalf("Wrong output range, got %d:%d expected %d:%d", outStart, outEnd, start, end)
		}
	}
}

func TestListCmd(t *testing.T) {
	withTestTerminal("testvariables", t, func(term *FakeTerminal) {
		term.MustExec("continue")
		term.MustExec("continue")
		listIsAt(t, term, "list", 27, 22, 32)
		listIsAt(t, term, "list 69", 69, 64, 74)
		listIsAt(t, term, "frame 1 list", 66, 61, 71)
		listIsAt(t, term, "frame 1 list 69", 69, 64, 74)
		_, err := term.Exec("frame 50 list")
		if err == nil {
			t.Fatalf("Expected error requesting 50th frame")
		}
		listIsAt(t, term, "list testvariables.go:1", -1, 1, 6)
		listIsAt(t, term, "list testvariables.go:10000", -1, 0, 0)
	})
}

func TestReverseContinue(t *testing.T) {
	test.AllowRecording(t)
	if testBackend != "rr" {
		return
	}
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		term.MustExec("break main.sayhi")
		listIsAt(t, term, "continue", 16, -1, -1)
		listIsAt(t, term, "continue", 12, -1, -1)
		listIsAt(t, term, "rewind", 16, -1, -1)
	})
}

func TestCheckpoints(t *testing.T) {
	test.AllowRecording(t)
	if testBackend != "rr" {
		return
	}
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		listIsAt(t, term, "continue", 16, -1, -1)
		term.MustExec("checkpoint")
		term.MustExec("checkpoints")
		listIsAt(t, term, "next", 17, -1, -1)
		listIsAt(t, term, "next", 18, -1, -1)
		term.MustExec("restart c1")
		term.MustExec("goroutine 1")
		listIsAt(t, term, "list", 16, -1, -1)
	})
}

func TestNextWithCount(t *testing.T) {
	test.AllowRecording(t)
	withTestTerminal("nextcond", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		listIsAt(t, term, "continue", 8, -1, -1)
		listIsAt(t, term, "next 2", 10, -1, -1)
	})
}

func TestRestart(t *testing.T) {
	withTestTerminal("restartargs", t, func(term *FakeTerminal) {
		term.MustExec("break main.printArgs")
		term.MustExec("continue")
		if out := term.MustExec("print main.args"); !strings.Contains(out, ", []") {
			t.Fatalf("wrong args: %q", out)
		}
		// Reset the arg list
		term.MustExec("restart hello")
		term.MustExec("continue")
		if out := term.MustExec("print main.args"); !strings.Contains(out, ", [\"hello\"]") {
			t.Fatalf("wrong args: %q ", out)
		}
		// Restart w/o arg should retain the current args.
		term.MustExec("restart")
		term.MustExec("continue")
		if out := term.MustExec("print main.args"); !strings.Contains(out, ", [\"hello\"]") {
			t.Fatalf("wrong args: %q ", out)
		}
		// Empty arg list
		term.MustExec("restart -noargs")
		term.MustExec("continue")
		if out := term.MustExec("print main.args"); !strings.Contains(out, ", []") {
			t.Fatalf("wrong args: %q ", out)
		}
	})
}

func TestIssue827(t *testing.T) {
	// switching goroutines when the current thread isn't running any goroutine
	// causes nil pointer dereference.
	withTestTerminal("notify-v2", t, func(term *FakeTerminal) {
		go func() {
			time.Sleep(1 * time.Second)
			resp, err := http.Get("http://127.0.0.1:8888/test")
			if err == nil {
				resp.Body.Close()
			}
			time.Sleep(1 * time.Second)
			term.client.Halt()
		}()
		term.MustExec("continue")
		term.MustExec("goroutine 1")
	})
}

func findCmdName(c *Commands, cmdstr string, prefix cmdPrefix) string {
	for _, v := range c.cmds {
		if v.match(cmdstr) {
			if prefix != noPrefix && v.allowedPrefixes&prefix == 0 {
				continue
			}
			return v.aliases[0]
		}
	}
	return ""
}

func assertNoError(t *testing.T, err error, str string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", str, err)
	}
}

func assertNoErrorConfigureCmd(t *testing.T, term *Term, cmdstr string) {
	t.Helper()
	err := configureCmd(term, callContext{}, cmdstr)
	assertNoError(t, err, fmt.Sprintf("error executing configureCmd(%s)", cmdstr))
}

func assertSubstitutePath(t *testing.T, sp config.SubstitutePathRules, v ...string) {
	t.Helper()
	if len(sp) != len(v)/2 {
		t.Fatalf("wrong number of substitute path rules (expected: %d): %#v", len(v)/2, sp)
	}
	for i := range sp {
		if sp[i].From != v[i*2] || sp[i].To != v[i*2+1] {
			t.Fatalf("wrong substitute path rule %#v expected (from: %q to %q)", sp[i], v[i*2], v[i*2+1])
		}
	}
}

func assertDebugInfoDirs(t *testing.T, got []string, tgt ...string) {
	if len(got) != len(tgt) {
		t.Fatalf("wrong number of debug info directories (got %d expected %d)", len(got), len(tgt))
	}
	for i := range got {
		if got[i] != tgt[i] {
			t.Fatalf("debug info directories mismatch got: %v expected: %v", got, tgt)
		}
	}
}

func TestConfig(t *testing.T) {
	var buf bytes.Buffer
	var term Term
	term.conf = &config.Config{}
	term.cmds = DebugCommands(nil)
	term.stdout = &transcriptWriter{pw: &pagingWriter{w: &buf}}

	err := configureCmd(&term, callContext{}, "nonexistent-parameter 10")
	if err == nil {
		t.Fatalf("expected error executing configureCmd(nonexistent-parameter)")
	}

	assertNoErrorConfigureCmd(t, &term, "max-string-len 10")
	if term.conf.MaxStringLen == nil {
		t.Fatalf("expected MaxStringLen 10, got nil")
	}
	if *term.conf.MaxStringLen != 10 {
		t.Fatalf("expected MaxStringLen 10, got: %d", *term.conf.MaxStringLen)
	}
	assertNoErrorConfigureCmd(t, &term, "show-location-expr   true")
	if term.conf.ShowLocationExpr != true {
		t.Fatalf("expected ShowLocationExpr true, got false")
	}

	assertNoErrorConfigureCmd(t, &term, "max-variable-recurse 4")
	if term.conf.MaxVariableRecurse == nil {
		t.Fatalf("expected MaxVariableRecurse 4, got nil")
	}
	if *term.conf.MaxVariableRecurse != 4 {
		t.Fatalf("expected MaxVariableRecurse 4, got: %d", *term.conf.MaxVariableRecurse)
	}

	assertNoErrorConfigureCmd(t, &term, "substitute-path a b")
	assertSubstitutePath(t, term.conf.SubstitutePath, "a", "b")

	assertNoErrorConfigureCmd(t, &term, "substitute-path a")
	assertSubstitutePath(t, term.conf.SubstitutePath)

	assertNoErrorConfigureCmd(t, &term, "alias print blah")
	if len(term.conf.Aliases["print"]) != 1 {
		t.Fatalf("aliases not changed after configure command %v", term.conf.Aliases)
	}
	if findCmdName(term.cmds, "blah", noPrefix) != "print" {
		t.Fatalf("new alias not found")
	}

	assertNoErrorConfigureCmd(t, &term, "alias blah")
	if len(term.conf.Aliases["print"]) != 0 {
		t.Fatalf("alias not removed after configure command %v", term.conf.Aliases)
	}
	if findCmdName(term.cmds, "blah", noPrefix) != "" {
		t.Fatalf("new alias found after delete")
	}

	err = configureCmd(&term, callContext{}, "show-location-expr")
	if err == nil {
		t.Fatalf("no error form configureCmd(show-location-expr)")
	}
	if !term.conf.ShowLocationExpr {
		t.Fatalf("ShowLocationExpr not set to true")
	}

	assertNoErrorConfigureCmd(t, &term, "show-location-expr false")
	if term.conf.ShowLocationExpr {
		t.Fatalf("ShowLocationExpr set to true")
	}

	assertNoErrorConfigureCmd(t, &term, "substitute-path a b")
	assertNoErrorConfigureCmd(t, &term, "substitute-path c d")
	assertSubstitutePath(t, term.conf.SubstitutePath, "a", "b", "c", "d")

	buf.Reset()
	assertNoErrorConfigureCmd(t, &term, "substitute-path")
	t.Logf("current substitute-path: %q", buf.String())
	if buf.String() != "\"a\" → \"b\"\n\"c\" → \"d\"\n" {
		t.Fatalf("wrong substitute-path value")
	}

	assertNoErrorConfigureCmd(t, &term, "substitute-path -clear c")
	assertSubstitutePath(t, term.conf.SubstitutePath, "a", "b")

	assertNoErrorConfigureCmd(t, &term, "substitute-path -clear")
	assertSubstitutePath(t, term.conf.SubstitutePath)

	assertNoErrorConfigureCmd(t, &term, "substitute-path \"\" something")
	assertSubstitutePath(t, term.conf.SubstitutePath, "", "something")

	assertNoErrorConfigureCmd(t, &term, "substitute-path somethingelse \"\"")
	assertSubstitutePath(t, term.conf.SubstitutePath, "", "something", "somethingelse", "")

	assertDebugInfoDirs(t, term.conf.DebugInfoDirectories)

	assertNoErrorConfigureCmd(t, &term, "debug-info-directories -add a")
	assertDebugInfoDirs(t, term.conf.DebugInfoDirectories, "a")
	assertNoErrorConfigureCmd(t, &term, "debug-info-directories -add b")
	assertDebugInfoDirs(t, term.conf.DebugInfoDirectories, "a", "b")
	assertNoErrorConfigureCmd(t, &term, "debug-info-directories -add c")
	assertDebugInfoDirs(t, term.conf.DebugInfoDirectories, "a", "b", "c")
	assertNoErrorConfigureCmd(t, &term, "debug-info-directories -rm b")
	assertDebugInfoDirs(t, term.conf.DebugInfoDirectories, "a", "c")
	assertNoErrorConfigureCmd(t, &term, "debug-info-directories -clear")
	assertDebugInfoDirs(t, term.conf.DebugInfoDirectories)
}

func TestIssue1090(t *testing.T) {
	// Exit while executing 'next' should report the "Process exited" error
	// message instead of crashing.
	withTestTerminal("math", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		term.MustExec("continue")
		for {
			_, err := term.Exec("next")
			if err != nil && strings.Contains(err.Error(), " has exited with status ") {
				break
			}
		}
	})
}

func TestPrintContextParkedGoroutine(t *testing.T) {
	if runtime.GOARCH == "ppc64le" && buildMode == "pie" {
		t.Skip("pie mode broken on ppc64le")
	}
	withTestTerminal("goroutinestackprog", t, func(term *FakeTerminal) {
		term.MustExec("break stacktraceme")
		term.MustExec("continue")

		// pick a goroutine that isn't running on a thread
		gid := ""
		gout := strings.Split(term.MustExec("goroutines"), "\n")
		t.Logf("goroutines -> %q", gout)
		for _, gline := range gout {
			if !strings.Contains(gline, "thread ") && strings.Contains(gline, "agoroutine") {
				if dash := strings.Index(gline, " - "); dash > 0 {
					gid = gline[len("  Goroutine "):dash]
					break
				}
			}
		}

		t.Logf("picked %q", gid)
		term.MustExec(fmt.Sprintf("goroutine %s", gid))

		frameout := strings.Split(term.MustExec("frame 0"), "\n")
		t.Logf("frame 0 -> %q", frameout)
		if strings.Contains(frameout[0], "stacktraceme") {
			t.Fatal("bad output for `frame 0` command on a parked goorutine")
		}

		listout := strings.Split(term.MustExec("list"), "\n")
		t.Logf("list -> %q", listout)
		if strings.Contains(listout[0], "stacktraceme") {
			t.Fatal("bad output for list command on a parked goroutine")
		}
	})
}

func TestStepOutReturn(t *testing.T) {
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		t.Skip("return variables aren't marked on 1.9 or earlier")
	}
	withTestTerminal("stepoutret", t, func(term *FakeTerminal) {
		term.MustExec("break main.stepout")
		term.MustExec("continue")
		out := term.MustExec("stepout")
		t.Logf("output: %q", out)
		if !strings.Contains(out, "num: ") || !strings.Contains(out, "str: ") {
			t.Fatal("could not find parameter")
		}
	})
}

func TestOptimizationCheck(t *testing.T) {
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		out := term.MustExec("continue")
		t.Logf("output %q", out)
		if strings.Contains(out, optimizedFunctionWarning) {
			t.Fatal("optimized function warning")
		}
	})

	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 10) {
		withTestTerminalBuildFlags("continuetestprog", t, test.EnableOptimization|test.EnableInlining, func(term *FakeTerminal) {
			term.MustExec("break main.main")
			out := term.MustExec("continue")
			t.Logf("output %q", out)
			if !strings.Contains(out, optimizedFunctionWarning) {
				t.Fatal("optimized function warning missing")
			}
		})
	}
}

func TestTruncateStacktrace(t *testing.T) {
	if runtime.GOARCH == "ppc64le" && buildMode == "pie" {
		t.Skip("pie mode broken on ppc64le")
	}
	const stacktraceTruncatedMessage = "(truncated)"
	withTestTerminal("stacktraceprog", t, func(term *FakeTerminal) {
		term.MustExec("break main.stacktraceme")
		term.MustExec("continue")
		out1 := term.MustExec("stack")
		t.Logf("untruncated output %q", out1)
		if strings.Contains(out1, stacktraceTruncatedMessage) {
			t.Fatalf("stacktrace was truncated")
		}
		out2 := term.MustExec("stack 1")
		t.Logf("truncated output %q", out2)
		if !strings.Contains(out2, stacktraceTruncatedMessage) {
			t.Fatalf("stacktrace was not truncated")
		}
	})
}

func TestIssue1493(t *testing.T) {
	// The 'regs' command without the '-a' option should only return
	// general purpose registers.
	if runtime.GOARCH == "ppc64le" {
		t.Skip("skipping, some registers such as vector registers are currently not loaded")
	}
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		r := term.MustExec("regs")
		nr := len(strings.Split(r, "\n"))
		t.Logf("regs: %s", r)
		ra := term.MustExec("regs -a")
		nra := len(strings.Split(ra, "\n"))
		t.Logf("regs -a: %s", ra)
		if nr > nra/2+1 {
			t.Fatalf("'regs' returned too many registers (%d) compared to 'regs -a' (%d)", nr, nra)
		}
	})
}

func findStarFile(name string) string {
	return filepath.Join(test.FindFixturesDir(), name+".star")
}

func TestIssue1598(t *testing.T) {
	if buildMode == "pie" && runtime.GOARCH == "ppc64le" {
		t.Skip("Debug function call Test broken in PIE mode")
	}
	test.MustSupportFunctionCalls(t, testBackend)
	withTestTerminal("issue1598", t, func(term *FakeTerminal) {
		term.MustExec("break issue1598.go:5")
		term.MustExec("continue")
		term.MustExec("config max-string-len 500")
		r := term.MustExec("call x()")
		t.Logf("result %q", r)
		if !strings.Contains(r, "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut \\nlabore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut") {
			t.Fatalf("wrong value returned")
		}
	})
}

func TestExamineMemoryCmd(t *testing.T) {
	withTestTerminal("examinememory", t, func(term *FakeTerminal) {
		term.MustExec("break examinememory.go:19")
		term.MustExec("break examinememory.go:24")
		term.MustExec("continue")

		addressStr := strings.TrimSpace(term.MustExec("p bspUintptr"))
		address, err := strconv.ParseInt(addressStr, 0, 64)
		if err != nil {
			t.Fatalf("could convert %s into int64, err %s", addressStr, err)
		}

		res := term.MustExec("examinemem  -count 52 -fmt hex " + addressStr)
		t.Logf("the result of examining memory \n%s", res)
		// check first line
		firstLine := fmt.Sprintf("%#x:   0x0a   0x0b   0x0c   0x0d   0x0e   0x0f   0x10   0x11", address)
		if !strings.Contains(res, firstLine) {
			t.Fatalf("expected first line: %s", firstLine)
		}

		// check last line
		lastLine := fmt.Sprintf("%#x:   0x3a   0x3b   0x3c   0x00", address+6*8)
		if !strings.Contains(res, lastLine) {
			t.Fatalf("expected last line: %s", lastLine)
		}

		// second examining memory
		term.MustExec("continue")
		res = term.MustExec("x -count 52 -fmt bin " + addressStr)
		t.Logf("the second result of examining memory result \n%s", res)

		// check first line
		firstLine = fmt.Sprintf("%#x:   11111111   00001011   00001100   00001101", address)
		if !strings.Contains(res, firstLine) {
			t.Fatalf("expected first line: %s", firstLine)
		}

		// third examining memory: -x addr
		res = term.MustExec("examinemem -x " + addressStr)
		t.Logf("the third result of examining memory result \n%s", res)
		firstLine = fmt.Sprintf("%#x:   0xff", address)
		if !strings.Contains(res, firstLine) {
			t.Fatalf("expected first line: %s", firstLine)
		}

		// fourth examining memory: -x addr + offset
		res = term.MustExec("examinemem -x " + addressStr + " + 8")
		t.Logf("the fourth result of examining memory result \n%s", res)
		firstLine = fmt.Sprintf("%#x:   0x12", address+8)
		if !strings.Contains(res, firstLine) {
			t.Fatalf("expected first line: %s", firstLine)
		}
		// fifth examining memory: -x &var
		res = term.MustExec("examinemem -x &bs[0]")
		t.Logf("the fifth result of examining memory result \n%s", res)
		firstLine = fmt.Sprintf("%#x:   0xff", address)
		if !strings.Contains(res, firstLine) {
			t.Fatalf("expected first line: %s", firstLine)
		}

		// sixth examining memory: -fmt and double spaces
		res = term.MustExec("examinemem -fmt  hex  -x &bs[0]")
		t.Logf("the sixth result of examining memory result \n%s", res)
		firstLine = fmt.Sprintf("%#x:   0xff", address)
		if !strings.Contains(res, firstLine) {
			t.Fatalf("expected first line: %s", firstLine)
		}
	})

	withTestTerminal("testvariables2", t, func(term *FakeTerminal) {
		tests := []struct {
			Expr string
			Want int
		}{
			{Expr: "&i1", Want: 1},
			{Expr: "&i2", Want: 2},
			{Expr: "p1", Want: 1},
			{Expr: "*pp1", Want: 1},
			{Expr: "&str1[1]", Want: '1'},
			{Expr: "c1.pb", Want: 1},
			{Expr: "&c1.pb.a", Want: 1},
			{Expr: "&c1.pb.a.A", Want: 1},
			{Expr: "&c1.pb.a.B", Want: 2},
		}
		term.MustExec("continue")
		for _, test := range tests {
			res := term.MustExec("examinemem -fmt dec -x " + test.Expr)
			// strip addr from output, e.g. "0xc0000160b8:   023" -> "023"
			res = strings.TrimSpace(strings.Split(res, ":")[1])
			got, err := strconv.Atoi(res)
			if err != nil {
				t.Fatalf("expr=%q err=%s", test.Expr, err)
			} else if got != test.Want {
				t.Errorf("expr=%q got=%d want=%d", test.Expr, got, test.Want)
			}
		}
	})
}

func TestPrintOnTracepoint(t *testing.T) {
	withTestTerminal("increment", t, func(term *FakeTerminal) {
		term.MustExec("trace main.Increment")
		term.MustExec("on 1 print y+1")
		out, _ := term.Exec("continue")
		if !strings.Contains(out, "y+1: 4") || !strings.Contains(out, "y+1: 2") || !strings.Contains(out, "y+1: 1") {
			t.Errorf("output did not contain breakpoint information: %q", out)
		}
	})
}

func TestPrintCastToInterface(t *testing.T) {
	withTestTerminal("testvariables2", t, func(term *FakeTerminal) {
		term.MustExec("continue")
		out := term.MustExec(`p (*"interface {}")(uintptr(&iface2))`)
		t.Logf("%q", out)
	})
}

func TestParseNewArgv(t *testing.T) {
	testCases := []struct {
		in       string
		tgtargs  string
		tgtredir string
		tgterr   string
	}{
		{"-noargs", "", " |  | ", ""},
		{"-noargs arg1", "", "", "too many arguments to restart"},
		{"arg1 arg2", "arg1 | arg2", " |  | ", ""},
		{"arg1 arg2 <input.txt", "arg1 | arg2", "input.txt |  | ", ""},
		{"arg1 arg2 < input.txt", "arg1 | arg2", "input.txt |  | ", ""},
		{"<input.txt", "", "input.txt |  | ", ""},
		{"< input.txt", "", "input.txt |  | ", ""},
		{"arg1 < input.txt > output.txt 2> error.txt", "arg1", "input.txt | output.txt | error.txt", ""},
		{"< input.txt > output.txt 2> error.txt", "", "input.txt | output.txt | error.txt", ""},
		{"arg1 <input.txt >output.txt 2>error.txt", "arg1", "input.txt | output.txt | error.txt", ""},
		{"<input.txt >output.txt 2>error.txt", "", "input.txt | output.txt | error.txt", ""},
		{"<input.txt <input2.txt", "", "", "redirect error: stdin redirected twice"},
	}

	for _, tc := range testCases {
		resetArgs, newArgv, newRedirects, err := parseNewArgv(tc.in)
		t.Logf("%q -> %q %q %v\n", tc.in, newArgv, newRedirects, err)
		if tc.tgterr != "" {
			if err == nil {
				t.Errorf("Expected error %q, got no error", tc.tgterr)
			} else if errstr := err.Error(); errstr != tc.tgterr {
				t.Errorf("Expected error %q, got error %q", tc.tgterr, errstr)
			}
		} else {
			if !resetArgs {
				t.Errorf("parse error, resetArgs is false")
				continue
			}
			argvstr := strings.Join(newArgv, " | ")
			if argvstr != tc.tgtargs {
				t.Errorf("Expected new arguments %q, got %q", tc.tgtargs, argvstr)
			}
			redirstr := strings.Join(newRedirects[:], " | ")
			if redirstr != tc.tgtredir {
				t.Errorf("Expected new redirects %q, got %q", tc.tgtredir, redirstr)
			}
		}
	}
}

func TestContinueUntil(t *testing.T) {
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		if runtime.GOARCH != "386" {
			listIsAt(t, term, "continue main.main", 16, -1, -1)
		} else {
			listIsAt(t, term, "continue main.main", 17, -1, -1)
		}
		listIsAt(t, term, "continue main.sayhi", 12, -1, -1)
	})
}

func TestContinueUntilExistingBreakpoint(t *testing.T) {
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		if runtime.GOARCH != "386" {
			listIsAt(t, term, "continue main.main", 16, -1, -1)
		} else {
			listIsAt(t, term, "continue main.main", 17, -1, -1)
		}
		listIsAt(t, term, "continue main.sayhi", 12, -1, -1)
	})
}

func TestPrintFormat(t *testing.T) {
	withTestTerminal("testvariables2", t, func(term *FakeTerminal) {
		term.MustExec("continue")
		out := term.MustExec("print %#x m2[1].B")
		if !strings.Contains(out, "0xb\n") {
			t.Fatalf("output did not contain '0xb': %q", out)
		}
	})
}

func TestHitCondBreakpoint(t *testing.T) {
	withTestTerminal("break", t, func(term *FakeTerminal) {
		term.MustExec("break bp1 main.main:4")
		term.MustExec("condition -hitcount bp1 > 2")
		listIsAt(t, term, "continue", 7, -1, -1)
		out := term.MustExec("print i")
		t.Logf("%q", out)
		if !strings.Contains(out, "3\n") {
			t.Fatalf("wrong value of i")
		}
	})

	withTestTerminal("condperghitcount", t, func(term *FakeTerminal) {
		term.MustExec("break bp1 main.main:8")
		term.MustExec("condition -per-g-hitcount bp1 == 2")
		listIsAt(t, term, "continue", 16, -1, -1)
		// first g hit
		out := term.MustExec("print j")
		t.Logf("%q", out)
		if !strings.Contains(out, "2\n") {
			t.Fatalf("wrong value of j")
		}
		term.MustExec("toggle bp1")
		listIsAt(t, term, "continue", 16, -1, -1)
		// second g hit
		out = term.MustExec("print j")
		t.Logf("%q", out)
		if !strings.Contains(out, "2\n") {
			t.Fatalf("wrong value of j")
		}
	})
}

func TestCondBreakpointWithFrame(t *testing.T) {
	withTestTerminal("condframe", t, func(term *FakeTerminal) {
		term.MustExec("break bp1 callme2")
		term.MustExec("condition bp1 runtime.frame(1).i == 3")
		term.MustExec("continue")
		out := term.MustExec("frame 1 print i")
		t.Logf("%q", out)
		if !strings.Contains(out, "3\n") {
			t.Fatalf("wrong value of i")
		}
	})
}

func TestClearCondBreakpoint(t *testing.T) {
	withTestTerminal("break", t, func(term *FakeTerminal) {
		term.MustExec("break main.main:4")
		term.MustExec("condition 1 i%3==2")
		listIsAt(t, term, "continue", 7, -1, -1)
		out := term.MustExec("print i")
		t.Logf("%q", out)
		if !strings.Contains(out, "2\n") {
			t.Fatalf("wrong value of i")
		}
		term.MustExec("condition -clear 1")
		listIsAt(t, term, "continue", 7, -1, -1)
		out = term.MustExec("print i")
		t.Logf("%q", out)
		if !strings.Contains(out, "3\n") {
			t.Fatalf("wrong value of i")
		}
	})
}

func TestBreakpointEditing(t *testing.T) {
	term := &FakeTerminal{
		t:    t,
		Term: New(nil, &config.Config{}),
	}
	_ = term

	var testCases = []struct {
		inBp    *api.Breakpoint
		inBpStr string
		edit    string
		outBp   *api.Breakpoint
	}{
		{ // tracepoint -> breakpoint
			&api.Breakpoint{Tracepoint: true},
			"trace",
			"",
			&api.Breakpoint{}},
		{ // breakpoint -> tracepoint
			&api.Breakpoint{Variables: []string{"a"}},
			"print a",
			"print a\ntrace",
			&api.Breakpoint{Tracepoint: true, Variables: []string{"a"}}},
		{ // add print var
			&api.Breakpoint{Variables: []string{"a"}},
			"print a",
			"print b\nprint a\n",
			&api.Breakpoint{Variables: []string{"b", "a"}}},
		{ // add goroutine flag
			&api.Breakpoint{},
			"",
			"goroutine",
			&api.Breakpoint{Goroutine: true}},
		{ // remove goroutine flag
			&api.Breakpoint{Goroutine: true},
			"goroutine",
			"",
			&api.Breakpoint{}},
		{ // add stack directive
			&api.Breakpoint{},
			"",
			"stack 10",
			&api.Breakpoint{Stacktrace: 10}},
		{ // remove stack directive
			&api.Breakpoint{Stacktrace: 20},
			"stack 20",
			"print a",
			&api.Breakpoint{Variables: []string{"a"}}},
		{ // add condition
			&api.Breakpoint{Variables: []string{"a"}},
			"print a",
			"print a\ncond a < b",
			&api.Breakpoint{Variables: []string{"a"}, Cond: "a < b"}},
		{ // remove condition
			&api.Breakpoint{Cond: "a < b"},
			"cond a < b",
			"",
			&api.Breakpoint{}},
		{ // change condition
			&api.Breakpoint{Cond: "a < b"},
			"cond a < b",
			"cond a < 5",
			&api.Breakpoint{Cond: "a < 5"}},
		{ // change hitcount condition
			&api.Breakpoint{HitCond: "% 2"},
			"cond -hitcount % 2",
			"cond -hitcount = 2",
			&api.Breakpoint{HitCond: "= 2"}},
	}

	for _, tc := range testCases {
		bp := *tc.inBp
		bpStr := strings.Join(formatBreakpointAttrs("", &bp, true), "\n")
		if bpStr != tc.inBpStr {
			t.Errorf("Expected %q got %q for:\n%#v", tc.inBpStr, bpStr, tc.inBp)
		}
		ctx := callContext{Prefix: onPrefix, Scope: api.EvalScope{GoroutineID: -1, Frame: 0, DeferredCall: 0}, Breakpoint: &bp}
		err := term.cmds.parseBreakpointAttrs(nil, ctx, strings.NewReader(tc.edit))
		if err != nil {
			t.Errorf("Unexpected error during edit %q", tc.edit)
		}
		if !reflect.DeepEqual(bp, *tc.outBp) {
			t.Errorf("mismatch after edit\nexpected: %#v\ngot: %#v", tc.outBp, bp)
		}
	}
}

func TestTranscript(t *testing.T) {
	withTestTerminal("math", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		out := term.MustExec("continue")
		if !strings.HasPrefix(out, "> main.main()") {
			t.Fatalf("Wrong output for next: <%s>", out)
		}
		fh, err := os.CreateTemp("", "test-transcript-*")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		name := fh.Name()
		fh.Close()
		t.Logf("output to %q", name)

		slurp := func() string {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("could not read transcript file: %v", err)
			}
			return string(b)
		}

		term.MustExec(fmt.Sprintf("transcript %s", name))
		out = term.MustExec("list")
		//term.MustExec("transcript -off")
		if out != slurp() {
			t.Logf("output of list %s", out)
			t.Logf("contents of transcript: %s", slurp())
			t.Errorf("transcript and command out differ")
		}

		term.MustExec(fmt.Sprintf("transcript -t -x %s", name))
		out = term.MustExec(`print "hello"`)
		if out != "" {
			t.Errorf("output of print is %q but should have been suppressed by transcript", out)
		}
		if slurp() != "\"hello\"\n" {
			t.Errorf("wrong contents of transcript: %q", slurp())
		}

		os.Remove(name)
	})
}

func TestDisassPosCmd(t *testing.T) {
	if runtime.GOARCH == "ppc64le" && buildMode == "pie" {
		t.Skip("pie mode broken on ppc64le")
	}
	withTestTerminal("testvariables2", t, func(term *FakeTerminal) {
		term.MustExec("continue")
		out := term.MustExec("step-instruction")
		t.Logf("%q\n", out)
		if !strings.Contains(out, "call $runtime.Breakpoint") && !strings.Contains(out, "CALL runtime.Breakpoint(SB)") {
			t.Errorf("output doesn't look like disassembly")
		}
	})
}

func TestCreateBreakpointByLocExpr(t *testing.T) {
	withTestTerminal("math", t, func(term *FakeTerminal) {
		out := term.MustExec("break main.main")
		position1 := strings.Split(out, " set at ")[1]
		term.MustExec("continue")
		term.MustExec("clear 1")
		out = term.MustExec("break +0")
		position2 := strings.Split(out, " set at ")[1]
		if position1 != position2 {
			t.Fatalf("mismatched positions %q and %q\n", position1, position2)
		}
	})
}

func TestRestartBreakpoints(t *testing.T) {
	// Tests that breakpoints set using just a line number and with a line
	// offset are preserved after restart. See issue #3423.
	withTestTerminal("continuetestprog", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		term.MustExec("continue")
		term.MustExec("break 9")
		term.MustExec("break +1")
		out := term.MustExec("breakpoints")
		t.Log("breakpoints before:\n", out)
		term.MustExec("restart")
		out = term.MustExec("breakpoints")
		t.Log("breakpoints after:\n", out)
		bps, err := term.client.ListBreakpoints(false)
		assertNoError(t, err, "ListBreakpoints")
		for _, bp := range bps {
			if bp.ID < 0 {
				continue
			}
			if bp.Addr == 0 {
				t.Fatalf("breakpoint %d has address 0", bp.ID)
			}
		}
	})
}

func TestListPackages(t *testing.T) {
	test.AllowRecording(t)
	withTestTerminal("goroutinestackprog", t, func(term *FakeTerminal) {
		out := term.MustExec("packages")
		t.Logf("> packages\n%s", out)
		seen := map[string]bool{}
		for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
			seen[p] = true
		}
		if !seen["main"] || !seen["runtime"] {
			t.Error("output omits 'main' and 'runtime'")
		}

		out = term.MustExec("packages runtime")
		t.Logf("> packages runtime\n%s", out)

		for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
			if !strings.Contains(p, "runtime") {
				t.Errorf("output includes unexpected %q", p)
			}
			seen[p] = true
		}
		if !seen["runtime"] {
			t.Error("output omits 'runtime'")
		}
	})
}

func TestSubstitutePathAndList(t *testing.T) {
	// checks that substitute path rules do not remain cached after a -clear.
	// See issue #3565.
	if runtime.GOOS == "windows" {
		t.Skip("test is not valid on windows due to path separators")
	}
	withTestTerminal("math", t, func(term *FakeTerminal) {
		term.MustExec("break main.main")
		term.MustExec("continue")
		fixturesDir, _ := filepath.Abs(test.FindFixturesDir())
		term.MustExec("config substitute-path " + fixturesDir + " /blah")
		out, _ := term.Exec("list")
		t.Logf("list output %s", out)
		if !strings.Contains(out, "/blah/math.go") {
			t.Fatalf("bad output")
		}
		term.MustExec("config substitute-path -clear")
		term.MustExec("config substitute-path " + fixturesDir + " /blah2")
		out, _ = term.Exec("list")
		t.Logf("list output %s", out)
		if !strings.Contains(out, "/blah2/math.go") {
			t.Fatalf("bad output")
		}
	})
}

func TestDisplay(t *testing.T) {
	// Tests that display command works. See issue #3595.
	type testCase struct{ in, tgt string }
	withTestTerminal("testvariables2", t, func(term *FakeTerminal) {
		term.MustExec("continue")

		for _, tc := range []testCase{
			{"string(byteslice)", `0: string(byteslice) = "tèst"`},
			{"string(byteslice[1:])", `0: string(byteslice[1:]) = "èst"`},
			{"%s string(byteslice)", `0: string(byteslice) = tèst`},
		} {
			out := term.MustExec("display -a " + tc.in)
			t.Logf("%q -> %q", tc.in, out)
			if !strings.Contains(out, tc.tgt) {
				t.Errorf("wrong output for 'display -a %s':\n\tgot: %q\n\texpected: %q", tc.in, out, tc.tgt)
			}
			term.MustExec("display -d 0")
		}
	})
}
