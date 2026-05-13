package main

import "fmt"

type SimpleStruct struct {
	A int
	B bool
	C float64
}

type PtrStruct struct {
	X int
	Y *int
	Z string
}

type Inner struct {
	V int
}

type Outer struct {
	Name  string
	Inner *Inner
}

type BigStruct struct {
	Data [9000]byte
	Tag  int
}

//go:noinline
func tracedPointer(p *int, u *uint64) {
	fmt.Println(p, u)
}

//go:noinline
func tracedSlice(s []byte) {
	fmt.Println(s)
}

//go:noinline
func tracedSmallInts(i8 int8, u16 uint16, i32 int32) {
	fmt.Println(i8, u16, i32)
}

//go:noinline
func tracedStruct(s SimpleStruct) {
	fmt.Println(s)
}

//go:noinline
func tracedPtrStruct(s PtrStruct) {
	fmt.Println(s)
}

//go:noinline
func tracedNestedStruct(o Outer) {
	fmt.Println(o)
}

//go:noinline
func tracedArray(a [4]int) {
	fmt.Println(a)
}

//go:noinline
func tracedArrayOfStructs(a [2]SimpleStruct) {
	fmt.Println(a)
}

//go:noinline
func tracedSliceOfStructs(s []SimpleStruct) {
	fmt.Println(s)
}

//go:noinline
func tracedBigStruct(b BigStruct) {
	fmt.Println(b.Tag)
}

//go:noinline
func tracedNilPtr(s PtrStruct) {
	fmt.Println(s)
}

func main() {
	x := 42
	y := uint64(100)
	tracedPointer(&x, &y)
	tracedSlice([]byte{1, 2, 3})
	tracedSmallInts(7, 1000, -42)

	tracedStruct(SimpleStruct{A: 42, B: true, C: 3.14})

	yy := 99
	tracedPtrStruct(PtrStruct{X: 1, Y: &yy, Z: "hello"})

	tracedNestedStruct(Outer{Name: "outer", Inner: &Inner{V: 7}})

	tracedArray([4]int{10, 20, 30, 40})

	tracedArrayOfStructs([2]SimpleStruct{
		{A: 1, B: true, C: 1.1},
		{A: 2, B: false, C: 2.2},
	})

	tracedSliceOfStructs([]SimpleStruct{
		{A: 5, B: true, C: 5.5},
		{A: 6, B: false, C: 6.6},
	})

	tracedBigStruct(BigStruct{Tag: 999})

	tracedNilPtr(PtrStruct{X: 1, Y: nil, Z: ""})
}
