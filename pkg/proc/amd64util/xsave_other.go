//+build !amd64

package amd64util

func AMD64XstateMaxSize() int {
	return _XSTATE_MAX_KNOWN_SIZE
}
