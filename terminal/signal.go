// +build linux,!nosignal

package terminal

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

func signalCmd(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		for i := 0; i < 64; i++ {
			s := syscall.Signal(i).String()
			if !strings.HasPrefix(s, "signal ") {
				fmt.Printf("%4d: %s\n", i, s)
			}
		}
		return nil
	}
	sig, err := strconv.Atoi(args)
	if err != nil {
		return err
	}
	return sigcont(t, ctx, sig)
}
