package main

import (
	"fmt"
	"runtime"
)

type FooBar struct {
	Baz int
	Bur string
}

// same member names, different order / types
type FooBar2 struct {
	Bur int
	Baz string
}

type Nest struct {
	Level int
	Nest  *Nest
}

func barfoo() {
	a1 := "bur"
	runtime.Breakpoint()
	fmt.Println(a1)
}

func foobar(baz string, bar FooBar) {
	var (
		a1   = "foofoofoofoofoofoo"
		a2   = 6
		a3   = 7.23
		a4   = [2]int{1, 2}
		a5   = []int{1, 2, 3, 4, 5}
		a6   = FooBar{Baz: 8, Bur: "word"}
		a7   = &FooBar{Baz: 5, Bur: "strum"}
		a8   = FooBar2{Bur: 10, Baz: "feh"}
		a9   = (*FooBar)(nil)
		a10  = a1[2:5]
		a11  = [3]FooBar{{1, "a"}, {2, "b"}, {3, "c"}}
		a12  = []FooBar{{4, "d"}, {5, "e"}}
		a13  = []*FooBar{{6, "f"}, {7, "g"}, {8, "h"}}
		b1   = true
		b2   = false
		neg  = -1
		i8   = int8(1)
		u8   = uint8(255)
		u16  = uint16(65535)
		u32  = uint32(4294967295)
		u64  = uint64(18446744073709551615)
		up   = uintptr(5)
		f32  = float32(1.2)
		c64  = complex(float32(1), float32(2))
		c128 = complex(float64(2), float64(3))
		i32  = [2]int32{1, 2}
		f    = barfoo
		ms   = Nest{0, &Nest{1, &Nest{2, &Nest{3, &Nest{4, nil}}}}} // Test recursion capping
		ba   = make([]int, 200, 200)                                // Test array size capping
		mp   = map[int]interface{}{1: 42, 2: 43}
	)

	runtime.Breakpoint()
	barfoo()
	fmt.Println(a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, b1, b2, baz, neg, i8, u8, u16, u32, u64, up, f32, c64, c128, i32, bar, f, ms, ba, p1, mp)
}

var p1 = 10

func main() {
	foobar("bazburzum", FooBar{Baz: 10, Bur: "lorem"})
}
