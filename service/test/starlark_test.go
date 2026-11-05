package service_test

import (
	"strings"
	"testing"
	"time"

	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
)

func TestEvalStarlarkCancel(t *testing.T) {
	// The script runs for a bounded amount of time so that, if cancellation
	// doesn't get through, the test fails instead of hanging forever.
	const script = `
def main():
	deadline = time.now() + time.parse_duration("10s")
	while time.now() < deadline:
		pass
`
	withTestClient2("testnextprog", t, func(c service.Client) {
		done := make(chan error, 1)
		go func() {
			_, err := c.EvalStarlark(0, api.EvalScope{GoroutineID: -1}, normalLoadConfig, "cancel_test.star", script, api.EvalStarlarkReset)
			done <- err
		}()

		// Keep cancelling until EvalStarlark returns, this way the test doesn't
		// depend on the cancel request arriving after the evaluation started.
		var err error
	cancelLoop:
		for {
			select {
			case err = <-done:
				break cancelLoop
			default:
			}
			assertNoError(c.EvalStarlarkCancel(0, true), t, "EvalStarlarkCancel")
			time.Sleep(50 * time.Millisecond)
		}

		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected EvalStarlark to be interrupted, got: %v", err)
		}
	})
}
