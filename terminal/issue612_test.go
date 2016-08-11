// +build linux,!nosignal

package terminal

import (
	"fmt"
	"os"
	"testing"
)

func TestIssue612(t *testing.T) {
	withTestTerminal("issue612", t, func(term *FakeTerminal) {
		term.MustExec(fmt.Sprintf("signal %d", os.Interrupt))
	})
}
