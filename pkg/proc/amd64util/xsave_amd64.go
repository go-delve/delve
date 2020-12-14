package amd64util

import (
	"sync"
)

var xstateMaxSize int
var loadXstateMaxSizeOnce sync.Once

func cpuid(axIn, cxIn uint32) (axOut, bxOut, cxOut, dxOut uint32)

func AMD64XstateMaxSize() int {
	loadXstateMaxSizeOnce.Do(func() {
		// See Intel 64 and IA-32 Architecture Software Developer's Manual, Vol. 1
		// chapter 13.2 and Vol. 2A CPUID instruction for a description of all the
		// magic constants.

		_, _, cx, _ := cpuid(0x01, 0x00)

		if cx&(1<<26) == 0 { // Vol. 2A, Table 3-10, XSAVE enabled bit check
			// XSAVE not supported by this processor
			xstateMaxSize = _XSTATE_MAX_KNOWN_SIZE
			return
		}

		_, _, cx, _ = cpuid(0x0d, 0x00) // processor extended state enumeration main leaf
		xstateMaxSize = int(cx)
	})
	return xstateMaxSize
}
