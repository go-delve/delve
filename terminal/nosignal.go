// +build darwin windows nosignal

package terminal

func signalCmd(t *Term, ctx callContext, args string) error {
	return unavailable("signal")
}
