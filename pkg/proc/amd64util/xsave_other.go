//go:build !amd64

package amd64util

func AMD64XstateMaxSize() int {
	return _XSTATE_MAX_KNOWN_SIZE
}

func AMD64XstateZMMHi256Offset() int {
	// AVX-512 not supported
	return 0
}
