package gdbserial

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"unicode"
)

// Record uses rr to record the execution of the specified program and
// returns the trace directory's path.
func Record(cmd []string, wd string, quiet bool) (tracedir string, err error) {
	rfd, wfd, err := os.Pipe()
	if err != nil {
		return "", err
	}

	args := make([]string, 0, len(cmd)+2)
	args = append(args, "record", "--print-trace-dir=3")
	args = append(args, cmd...)
	rrcmd := exec.Command("rr", args...)
	rrcmd.Stdin = os.Stdin
	if !quiet {
		rrcmd.Stdout = os.Stdout
		rrcmd.Stderr = os.Stderr
	}
	rrcmd.ExtraFiles = []*os.File{wfd}
	rrcmd.Dir = wd

	done := make(chan struct{})
	go func() {
		bs, _ := ioutil.ReadAll(rfd)
		tracedir = strings.TrimSpace(string(bs))
		close(done)
	}()

	err = rrcmd.Run()
	// ignore run errors, it could be the program crashing
	wfd.Close()
	<-done
	return
}

// Replay starts an instance of rr in replay mode, with the specified trace
// directory, and connects to it.
func Replay(tracedir string, quiet bool) (*Process, error) {
	rrcmd := exec.Command("rr", "replay", "--dbgport=0", tracedir)
	rrcmd.Stdout = os.Stdout
	stderr, err := rrcmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	rrcmd.SysProcAttr = backgroundSysProcAttr()

	initch := make(chan rrInit)
	go rrStderrParser(stderr, initch, quiet)

	err = rrcmd.Start()
	if err != nil {
		return nil, err
	}

	init := <-initch
	if init.err != nil {
		rrcmd.Process.Kill()
		return nil, err
	}
	p, err := Connect(init.port, init.exe, 0, 10)
	if err != nil {
		rrcmd.Process.Kill()
		return nil, err
	}

	p.process = rrcmd
	p.tracedir = tracedir

	return p, nil
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

func rrStderrParser(stderr io.Reader, initch chan<- rrInit, quiet bool) {
	rd := bufio.NewReader(stderr)
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
				return rrInit{err: &ErrMalformedRRGdbCommand{line, "contents of -ex argument unexpected"}}
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
func RecordAndReplay(cmd []string, wd string, quiet bool) (p *Process, tracedir string, err error) {
	tracedir, err = Record(cmd, wd, quiet)
	if tracedir == "" {
		return nil, "", err
	}
	p, err = Replay(tracedir, quiet)
	return p, tracedir, err
}
