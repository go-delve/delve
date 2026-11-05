package debugger

import (
	"strings"
	"testing"

	"github.com/go-delve/delve/service/api"
)

var starlarkTestScope = api.EvalScope{GoroutineID: -1}

type starlarkTestResp struct {
	out api.EvalStarlarkOut
	err error
}

func newStarlarkTestDebugger() (*Debugger, chan starlarkTestResp, EvalStarlarkRespFunc) {
	d := &Debugger{}
	d.StarlarkEnv = starlarkEnvNew(d)
	resps := make(chan starlarkTestResp, 10)
	respFn := func(out api.EvalStarlarkOut, err error) {
		resps <- starlarkTestResp{out, err}
	}
	return d, resps, respFn
}

// startStarlarkTest calls EvalStarlark in a new goroutine, like the RPC
// server does, the returned channel receives the value of recover() once
// EvalStarlark returns.
func startStarlarkTest(d *Debugger, path, script string, respFn EvalStarlarkRespFunc) chan any {
	panicked := make(chan any, 1)
	go func() {
		defer func() {
			panicked <- recover()
		}()
		d.EvalStarlark(0, nil, starlarkTestScope, api.LoadConfig{}, path, script, api.EvalStarlarkReset, respFn)
	}()
	return panicked
}

func TestEvalStarlarkCancel_DuringClientRoundTrip_NoPanic(t *testing.T) {
	d, resps, respFn := newStarlarkTestDebugger()
	panicked := startStarlarkTest(d, "roundtrip.star", "def main():\n\tdlv_command(\"foo\")\n", respFn)

	r := <-resps
	if r.err != nil || r.out.Kind != api.StarlarkDlvCommand {
		t.Fatalf("expected dlv_command request, got: %#v %v", r.out, r.err)
	}

	d.EvalStarlarkCancel(r.out.ThreadID)
	if p := <-panicked; p != nil {
		t.Fatalf("EvalStarlark panicked after cancel: %v", p)
	}

	d.EvalStarlarkContinue(r.out.ThreadID, "", starlarkTestScope, api.LoadConfig{}, "", respFn)
	r = <-resps
	if r.err == nil || r.err.Error() != "unknown starlark thread" {
		t.Fatalf("expected unknown starlark thread error, got: %#v %v", r.out, r.err)
	}

	select {
	case r := <-resps:
		t.Fatalf("unexpected response: %#v %v", r.out, r.err)
	default:
	}
}

// TestEvalStarlarkContinue_MultipleRoundTrips_AllAnswered checks that every
// client round trip gets a response. Run with -race to also check the
// handoff of the response callback between goroutines.
func TestEvalStarlarkContinue_MultipleRoundTrips_AllAnswered(t *testing.T) {
	d, resps, respFn := newStarlarkTestDebugger()
	panicked := startStarlarkTest(d, "roundtrip.star", "def main():\n\tfor i in range(20):\n\t\tprint(i)\n", respFn)

	n := 0
	for {
		r := <-resps
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if r.out.Kind == api.StarlarkDone {
			break
		}
		if r.out.Kind != api.StarlarkPrint {
			t.Fatalf("unexpected response: %#v", r.out)
		}
		n++
		go d.EvalStarlarkContinue(r.out.ThreadID, "", starlarkTestScope, api.LoadConfig{}, "", respFn)
	}
	if p := <-panicked; p != nil {
		t.Fatalf("EvalStarlark panicked: %v", p)
	}
	if n != 20 {
		t.Fatalf("expected 20 print requests, got %d", n)
	}
}

// TestEvalStarlarkCancelAll_ConcurrentWithEval_NoRace checks that
// cancelling, which the RPC server does synchronously, can run concurrently
// with EvalStarlark, which the RPC server runs asynchronously. Run with -race.
func TestEvalStarlarkCancelAll_ConcurrentWithEval_NoRace(t *testing.T) {
	d, _, _ := newStarlarkTestDebugger()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			d.EvalStarlark(0, nil, starlarkTestScope, api.LoadConfig{}, api.StarlarkStdin, "1+1", 0, func(api.EvalStarlarkOut, error) {})
		}
	}()
	for range 50 {
		d.EvalStarlarkCancelAll()
	}
	<-done
}

func evalStarlarkNoninteractive(d *Debugger, resps chan starlarkTestResp, respFn EvalStarlarkRespFunc, script string) starlarkTestResp {
	d.EvalStarlark(0, nil, starlarkTestScope, api.LoadConfig{}, api.StarlarkStdin, script, api.EvalStarlarkNoninteractive, respFn)
	return <-resps
}

func TestEvalStarlarkNoninteractive_Expression_ReturnsOutput(t *testing.T) {
	d, resps, respFn := newStarlarkTestDebugger()
	r := evalStarlarkNoninteractive(d, resps, respFn, "1+2")
	if r.err != nil || r.out.Kind != api.StarlarkDone || r.out.Output != "3" {
		t.Fatalf("expected StarlarkDone with output \"3\", got: %#v %v", r.out, r.err)
	}
	d.StarlarkEnv.threadsMu.Lock()
	n := len(d.StarlarkEnv.threads)
	d.StarlarkEnv.threadsMu.Unlock()
	if n != 0 {
		t.Fatalf("expected no threads left after a non-interactive evaluation, got %d", n)
	}
}

func TestEvalStarlarkNoninteractive_RoundTripBuiltin_Fails(t *testing.T) {
	d, resps, respFn := newStarlarkTestDebugger()
	r := evalStarlarkNoninteractive(d, resps, respFn, `dlv_command("continue")`)
	const tgt = "dlv_command not allowed in this environment"
	if r.err == nil || !strings.Contains(r.err.Error(), tgt) {
		t.Fatalf("expected error containing %q, got: %#v %v", tgt, r.out, r.err)
	}
}

func TestEvalStarlarkNoninteractive_InteractiveThread_Rejected(t *testing.T) {
	d, resps, respFn := newStarlarkTestDebugger()
	d.EvalStarlark(0, nil, starlarkTestScope, api.LoadConfig{}, api.StarlarkStdin, "1", 0, respFn)
	r := <-resps
	if r.err != nil {
		t.Fatalf("unexpected error creating interactive thread: %v", r.err)
	}
	d.EvalStarlark(r.out.ThreadID, nil, starlarkTestScope, api.LoadConfig{}, api.StarlarkStdin, "1", api.EvalStarlarkNoninteractive, respFn)
	r = <-resps
	if r.err == nil {
		t.Fatalf("expected error, got: %#v %v", r.out, r.err)
	}
}
