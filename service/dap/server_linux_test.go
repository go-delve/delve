package dap

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/dap/daptest"
	godap "github.com/google/go-dap"
)

func TestAttachRequestWithInvalidProgram(t *testing.T) {
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		cmd := execFixture(t, fixture)
		defer func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}()
		program := filepath.Join(t.TempDir(), "missing-executable")
		client.InitializeRequest()
		client.ExpectInitializeResponseAndCapabilities(t)
		client.AttachRequest(map[string]any{
			"mode": "local", "processId": cmd.Process.Pid, "program": program,
			"backend": "native", "stopOnEntry": true,
		})
		// Check the first response without blocking on an error if the override
		// is ignored and the attach unexpectedly succeeds.
		msg := client.ExpectMessage(t)
		if response, ok := msg.(*godap.ErrorResponse); ok {
			if !checkErrorMessageId(response.Body.Error, FailedToAttach) ||
				!strings.Contains(response.Body.Error.Format, program) {
				t.Errorf("expected attach error naming %q, got %#v", program, response)
			}
			client.DisconnectRequest()
			client.ExpectDisconnectResponse(t)
		} else {
			t.Errorf("expected explicit missing program to fail attach, got %T", msg)
			client.ExpectInitializedEvent(t)
			client.ExpectAttachResponse(t)
			client.DisconnectRequestWithKillOption(true)
			client.ExpectOutputEvent(t)
			client.ExpectDisconnectResponse(t)
			client.ExpectTerminatedEvent(t)
		}
	})
}

func TestAttachRequestWithProcRootProgram(t *testing.T) {
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		cmd := execFixture(t, fixture)
		defer func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}()
		// Exercise the path form callers use across mount namespaces. This
		// target itself runs in the same mount namespace as the debugger.
		program := fmt.Sprintf("/proc/%d/root%s", cmd.Process.Pid, fixture.Path)
		runDebugSessionWithBPs(t, client, "attach", func() {
			client.AttachRequest(map[string]any{
				"mode": "local", "processId": cmd.Process.Pid, "program": program,
				"backend": "native", "stopOnEntry": false,
			})
			client.ExpectCapabilitiesEventSupportTerminateDebuggee(t)
		}, fixture.Source, []int{8}, []onBreakpoint{{
			execute: func() {
				checkStop(t, client, 1, "main.loop", 8)
			},
			disconnect: true,
		}})
	})
}
