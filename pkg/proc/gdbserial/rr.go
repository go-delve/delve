package gdbserial

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/intel-go/cpuid"
)

// RecordAsync configures rr to record the execution of the specified
// program. Returns a run function which will actually record the program, a
// stop function which will prematurely terminate the recording of the
// program.
func RecordAsync(cmd []string, wd string, quiet bool, redirects [3]string) (run func() (string, error), stop func() error, err error) {
	if err := CheckRRAvailable(); err != nil {
		return nil, nil, err
	}

	rfd, wfd, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	args := make([]string, 0, len(cmd)+2)
	args = append(args, "record", "--print-trace-dir=3")
	args = append(args, cmd...)
	rrcmd := exec.Command("rr", args...)
	var closefn func()
	rrcmd.Stdin, rrcmd.Stdout, rrcmd.Stderr, closefn, err = openRedirects(redirects, quiet)
	if err != nil {
		return nil, nil, err
	}
	rrcmd.ExtraFiles = []*os.File{wfd}
	rrcmd.Dir = wd

	tracedirChan := make(chan string)
	go func() {
		bs, _ := ioutil.ReadAll(rfd)
		tracedirChan <- strings.TrimSpace(string(bs))
	}()

	run = func() (string, error) {
		err := rrcmd.Run()
		closefn()
		_ = wfd.Close()
		tracedir := <-tracedirChan
		return tracedir, err
	}

	stop = func() error {
		return rrcmd.Process.Signal(syscall.SIGTERM)
	}

	return run, stop, nil
}

func openRedirects(redirects [3]string, quiet bool) (stdin, stdout, stderr *os.File, closefn func(), err error) {
	toclose := []*os.File{}

	if redirects[0] != "" {
		stdin, err = os.Open(redirects[0])
		if err != nil {
			return nil, nil, nil, nil, err
		}
		toclose = append(toclose, stdin)
	} else {
		stdin = os.Stdin
	}

	create := func(path string, dflt *os.File) *os.File {
		if path == "" {
			if quiet {
				return nil
			}
			return dflt
		}
		var f *os.File
		f, err = os.Create(path)
		if f != nil {
			toclose = append(toclose, f)
		}
		return f
	}

	stdout = create(redirects[1], os.Stdout)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	stderr = create(redirects[2], os.Stderr)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	closefn = func() {
		for _, f := range toclose {
			_ = f.Close()
		}
	}

	return stdin, stdout, stderr, closefn, nil
}

// Record uses rr to record the execution of the specified program and
// returns the trace directory's path.
func Record(cmd []string, wd string, quiet bool, redirects [3]string) (tracedir string, err error) {
	run, _, err := RecordAsync(cmd, wd, quiet, redirects)
	if err != nil {
		return "", err
	}

	// ignore run errors, it could be the program crashing
	return run()
}

// Replay starts an instance of rr in replay mode, with the specified trace
// directory, and connects to it.
func Replay(tracedir string, quiet, deleteOnDetach bool, debugInfoDirs []string) (*proc.Target, error) {
	if err := CheckRRAvailable(); err != nil {
		return nil, err
	}

	rrcmd := exec.Command("rr", "replay", "--dbgport=0", tracedir)
	rrcmd.Stdout = os.Stdout
	stderr, err := rrcmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	rrcmd.SysProcAttr = sysProcAttr(false)

	initch := make(chan rrInit)
	go rrStderrParser(stderr, initch, quiet)

	err = rrcmd.Start()
	if err != nil {
		return nil, err
	}

	init := <-initch
	if init.err != nil {
		rrcmd.Process.Kill()
		return nil, init.err
	}

	p := newProcess(rrcmd.Process)
	p.tracedir = tracedir
	if deleteOnDetach {
		p.onDetach = func() {
			safeRemoveAll(p.tracedir)
		}
	}
	tgt, err := p.Dial(init.port, init.exe, 0, debugInfoDirs, proc.StopLaunched)
	if err != nil {
		rrcmd.Process.Kill()
		return nil, err
	}

	return tgt, nil
}

// ErrPerfEventParanoid is the error returned by Reply and Record if
// /proc/sys/kernel/perf_event_paranoid is greater than 1.
type ErrPerfEventParanoid struct {
	actual int
}

func (err ErrPerfEventParanoid) Error() string {
	return fmt.Sprintf("rr needs /proc/sys/kernel/perf_event_paranoid <= 1, but it is %d", err.actual)
}

func CheckRRCompatible() error {
	if runtime.GOOS != "linux" {
		return errors.New("unsupported operative system for rr")
	}

	// Check for Intel Nehalem and posterior process model compatiblity
	// per https://github.com/rr-debugger/rr#system-requirements
	// This equals to CPU family 6, model 30 and up (see https://en.wikichip.org/wiki/intel/cpuid)
	if (cpuid.DisplayModel > 30) && (cpuid.DisplayFamily == 6) {
		return nil
	}

	// Check for AMD Zen model compatiblity
	// per https://github.com/rr-debugger/rr/wiki/Zen
	// This equals to CPU families 23 and 25 (see https://en.wikichip.org/wiki/amd/cpuid)
	// TODO (lggomez): We won't be performing here a thorough model comparison here so by default all Zen models are allowed
	if (cpuid.DisplayFamily == 23) || (cpuid.DisplayFamily == 25) {
		return nil
	}

	return errors.New("unsupported cpu family and/or model for rr")
}

func CheckRRAvailable() error {
	if _, err := exec.LookPath("rr"); err != nil {
		return &ErrBackendUnavailable{}
	}

	// Check that /proc/sys/kernel/perf_event_paranoid doesn't exist or is <= 1.
	buf, err := ioutil.ReadFile("/proc/sys/kernel/perf_event_paranoid")
	if err == nil {
		perfEventParanoid, _ := strconv.Atoi(strings.TrimSpace(string(buf)))
		if perfEventParanoid > 1 {
			return ErrPerfEventParanoid{perfEventParanoid}
		}
	}

	return nil
}

type rrInit struct {
	port string
	exe  string
	err  error
}

const (
	rrGdbCommandPrefix = "  gdb "
	rrGdbLaunchPrefix  = "Launch gdb with"
	targetCmd          = "target extended-remote "
)

func rrStderrParser(stderr io.ReadCloser, initch chan<- rrInit, quiet bool) {
	rd := bufio.NewReader(stderr)
	defer stderr.Close()

	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			initch <- rrInit{"", "", err}
			close(initch)
			return
		}

		if strings.HasPrefix(line, rrGdbCommandPrefix) {
			initch <- rrParseGdbCommand(line[len(rrGdbCommandPrefix):])
			close(initch)
			break
		}

		if strings.HasPrefix(line, rrGdbLaunchPrefix) {
			continue
		}

		if !quiet {
			os.Stderr.Write([]byte(line))
		}
	}

	io.Copy(os.Stderr, rd)
}

type ErrMalformedRRGdbCommand struct {
	line, reason string
}

func (err *ErrMalformedRRGdbCommand) Error() string {
	return fmt.Sprintf("malformed gdb command %q: %s", err.line, err.reason)
}

func rrParseGdbCommand(line string) rrInit {
	port := ""
	fields := splitQuotedFields(line)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "-ex":
			if i+1 >= len(fields) {
				return rrInit{err: &ErrMalformedRRGdbCommand{line, "-ex not followed by an argument"}}
			}
			arg := fields[i+1]

			if !strings.HasPrefix(arg, targetCmd) {
				continue
			}

			port = arg[len(targetCmd):]
			i++

		case "-l":
			// skip argument
			i++
		}
	}

	if port == "" {
		return rrInit{err: &ErrMalformedRRGdbCommand{line, "could not find -ex argument"}}
	}

	exe := fields[len(fields)-1]

	return rrInit{port: port, exe: exe}
}

// Like strings.Fields but ignores spaces inside areas surrounded
// by single quotes.
// To specify a single quote use backslash to escape it: '\''
func splitQuotedFields(in string) []string {
	type stateEnum int
	const (
		inSpace stateEnum = iota
		inField
		inQuote
		inQuoteEscaped
	)
	state := inSpace
	r := []string{}
	var buf bytes.Buffer

	for _, ch := range in {
		switch state {
		case inSpace:
			if ch == '\'' {
				state = inQuote
			} else if !unicode.IsSpace(ch) {
				buf.WriteRune(ch)
				state = inField
			}

		case inField:
			if ch == '\'' {
				state = inQuote
			} else if unicode.IsSpace(ch) {
				r = append(r, buf.String())
				buf.Reset()
			} else {
				buf.WriteRune(ch)
			}

		case inQuote:
			if ch == '\'' {
				state = inField
			} else if ch == '\\' {
				state = inQuoteEscaped
			} else {
				buf.WriteRune(ch)
			}

		case inQuoteEscaped:
			buf.WriteRune(ch)
			state = inQuote
		}
	}

	if buf.Len() != 0 {
		r = append(r, buf.String())
	}

	return r
}

// RecordAndReplay acts like calling Record and then Replay.
func RecordAndReplay(cmd []string, wd string, quiet bool, debugInfoDirs []string, redirects [3]string) (*proc.Target, string, error) {
	tracedir, err := Record(cmd, wd, quiet, redirects)
	if tracedir == "" {
		return nil, "", err
	}
	t, err := Replay(tracedir, quiet, true, debugInfoDirs)
	return t, tracedir, err
}

// safeRemoveAll removes dir and its contents but only as long as dir does
// not contain directories.
func safeRemoveAll(dir string) {
	dh, err := os.Open(dir)
	if err != nil {
		return
	}
	defer dh.Close()
	fis, err := dh.Readdir(-1)
	if err != nil {
		return
	}
	for _, fi := range fis {
		if fi.IsDir() {
			return
		}
	}
	for _, fi := range fis {
		if err := os.Remove(filepath.Join(dir, fi.Name())); err != nil {
			return
		}
	}
	os.Remove(dir)
}
