//go:build amd64 || 386

package cpuid

import (
	"sync"
)

const _XSTATE_MAX_KNOWN_SIZE = 2969


var xstateMaxSize int
var loadXstateMaxSizeOnce sync.Once

func cpuid(axIn, cxIn uint32) (axOut, bxOut, cxOut, dxOut uint32)

// AMD64XstateMaxSize returns the maximum size of the xstate area.
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

var xstateZMMHi256Offset int
var loadXstateZMMHi256OffsetOnce sync.Once

// AMD64XstateZMMHi256Offset probes ZMM_Hi256 offset of the current CPU.  Beware
// that core dumps may be generated from a different CPU.
func AMD64XstateZMMHi256Offset() int {
	loadXstateZMMHi256OffsetOnce.Do(func() {
		// See Intel 64 and IA-32 Architecture Software Developer's Manual, Vol. 1
		// chapter 13.2 and Vol. 2A CPUID instruction for a description of all the
		// magic constants.

		_, _, cx, _ := cpuid(0x01, 0x00)

		if cx&(1<<26) == 0 { // Vol. 2A, Table 3-10, XSAVE enabled bit check
			// XSAVE not supported by this processor
			xstateZMMHi256Offset = 0
			return
		}

		_, bx, _, _ := cpuid(0x0d, 0x06) // ZMM_Hi256 is component #6
		xstateZMMHi256Offset = int(bx)
	})
	return xstateZMMHi256Offset
}
