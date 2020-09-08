//+build linux darwin dragonfly freebsd netbsd openbsd solaris
//+build amd64 arm64,!darwin mips64x ppc64x

package starlark

// This file defines an optimized Int implementation for 64-bit machines
// running POSIX. It reserves a 4GB portion of the address space using
// mmap and represents int32 values as addresses within that range. This
// disambiguates int32 values from *big.Int pointers, letting all Int
// values be represented as an unsafe.Pointer, so that Int-to-Value
// interface conversion need not allocate.

// Although iOS (arm64,darwin) claims to be a POSIX-compliant,
// it limits each process to about 700MB of virtual address space,
// which defeats the optimization.
//
// TODO(golang.org/issue/38485): darwin,arm64 may refer to macOS in the future.
// Update this when there are distinct GOOS values for macOS, iOS, and other Apple
// operating systems on arm64.

import (
	"log"
	"math"
	"math/big"
	"runtime"
	"syscall"
	"unsafe"
)

// intImpl represents a union of (int32, *big.Int) in a single pointer,
// so that Int-to-Value conversions need not allocate.
//
// The pointer is either a *big.Int, if the value is big, or a pointer into a
// reserved portion of the address space (smallints), if the value is small.
//
// See int_generic.go for the basic representation concepts.
type intImpl unsafe.Pointer

// get returns the (small, big) arms of the union.
func (i Int) get() (int64, *big.Int) {
	ptr := uintptr(i.impl)
	if ptr >= smallints && ptr < smallints+1<<32 {
		return math.MinInt32 + int64(ptr-smallints), nil
	}
	return 0, (*big.Int)(i.impl)
}

// Precondition: math.MinInt32 <= x && x <= math.MaxInt32
func makeSmallInt(x int64) Int {
	return Int{intImpl(uintptr(x-math.MinInt32) + smallints)}
}

// Precondition: x cannot be represented as int32.
func makeBigInt(x *big.Int) Int { return Int{intImpl(x)} }

// smallints is the base address of a 2^32 byte memory region.
// Pointers to addresses in this region represent int32 values.
// We assume smallints is not at the very top of the address space.
var smallints = reserveAddresses(1 << 32)

func reserveAddresses(len int) uintptr {
	// Use syscall to avoid golang.org/x/sys/unix dependency.
	MAP_ANON := 0x1000 // darwin (and all BSDs)
	switch runtime.GOOS {
	case "linux", "android":
		MAP_ANON = 0x20
	case "solaris":
		MAP_ANON = 0x100
	}
	b, err := syscall.Mmap(-1, 0, len, syscall.PROT_READ, syscall.MAP_PRIVATE|MAP_ANON)
	if err != nil {
		log.Fatalf("mmap: %v", err)
	}
	return uintptr(unsafe.Pointer(&b[0]))
}
