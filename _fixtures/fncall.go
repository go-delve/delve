package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

var call = "this is a variable named `call`"

func callstacktrace() (stacktrace string) {
	for skip := 0; ; skip++ {
		pc, file, line, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		fn := runtime.FuncForPC(pc)
		stacktrace += fmt.Sprintf("in %s at %s:%d\n", fn.Name(), file, line)
	}
	return stacktrace
}

func call0(a, b int) {
	fmt.Printf("call0: first: %d second: %d\n", a, b)
}

func call1(a, b int) int {
	fmt.Printf("first: %d second: %d\n", a, b)
	return a + b
}

func call2(a, b int) (int, int) {
	fmt.Printf("call2: first: %d second: %d\n", a, b)
	return a, b
}

func callexit() {
	fmt.Printf("about to exit\n")
	os.Exit(0)
}

func callpanic() {
	fmt.Printf("about to panic\n")
	panic("callpanic panicked")
}

func callbreak() {
	fmt.Printf("about to break")
	runtime.Breakpoint()
}

func stringsJoin(v []string, sep string) string {
	// This is needed because strings.Join is in an optimized package and
	// because of a bug in the compiler arguments of optimized functions don't
	// have a location.
	return strings.Join(v, sep)
}

type astruct struct {
	X int
}

type a2struct struct {
	Y int
}

func (a astruct) VRcvr(x int) string {
	return fmt.Sprintf("%d + %d = %d", x, a.X, x+a.X)
}

func (pa *astruct) PRcvr(x int) string {
	return fmt.Sprintf("%d - %d = %d", x, pa.X, x-pa.X)
}

type PRcvrable interface {
	PRcvr(int) string
}

type VRcvrable interface {
	VRcvr(int) string
}

var zero = 0

func makeclos(pa *astruct) func(int) string {
	i := 0
	return func(x int) string {
		i++
		return fmt.Sprintf("%d + %d + %d = %d", i, pa.X, x, i+pa.X+x)
	}
}

var ga astruct
var globalPA2 *a2struct

func escapeArg(pa2 *a2struct) {
	globalPA2 = pa2
}

func square(x int) int {
	return x * x
}

func intcallpanic(a int) int {
	if a == 0 {
		panic("panic requested")
	}
	return a
}

func onetwothree(n int) []int {
	return []int{n + 1, n + 2, n + 3}
}

func curriedAdd(n int) func(int) int {
	return func(m int) int {
		return n + m
	}
}

func getAStruct(n int) astruct {
	return astruct{X: n}
}

func getAStructPtr(n int) *astruct {
	return &astruct{X: n}
}

func getVRcvrableFromAStruct(n int) VRcvrable {
	return astruct{X: n}
}

func getPRcvrableFromAStructPtr(n int) PRcvrable {
	return &astruct{X: n}
}

func getVRcvrableFromAStructPtr(n int) VRcvrable {
	return &astruct{X: n}
}

func noreturncall(n int) {
	return
}

type Base struct {
	y int
}

type Derived struct {
	x int
	Base
}

func (b *Base) Method() int {
	return b.y
}

type X int

func (_ X) CallMe() {
	println("foo")
}

type X2 int

func (_ X2) CallMe(i int) int {
	return i * i
}

func regabistacktest(s1, s2, s3, s4, s5 string, n uint8) (string, string, string, string, string, uint8) {
	return s1 + s2, s2 + s3, s3 + s4, s4 + s5, s5 + s1, 2 * n
}

func regabistacktest2(n1, n2, n3, n4, n5, n6, n7, n8, n9, n10 int) (int, int, int, int, int, int, int, int, int, int) {
	return n1 + n2, n2 + n3, n3 + n4, n4 + n5, n5 + n6, n6 + n7, n7 + n8, n8 + n9, n9 + n10, n10 + n1
}

type Issue2698 struct {
	a uint32
	b uint8
	c uint8
	d uintptr
}

func (i Issue2698) String() string {
	return fmt.Sprintf("%d %d %d %d", i.a, i.b, i.c, i.d)
}

func main() {
	one, two := 1, 2
	intslice := []int{1, 2, 3}
	stringslice := []string{"one", "two", "three"}
	comma := ","
	a := astruct{X: 3}
	pa := &astruct{X: 6}
	a2 := a2struct{Y: 7}
	var pa2 *astruct
	var str string = "old string value"
	longstrs := []string{"very long string 0123456789a0123456789b0123456789c0123456789d0123456789e0123456789f0123456789g012345678h90123456789i0123456789j0123456789"}
	var vable_a VRcvrable = a
	var vable_pa VRcvrable = pa
	var pable_pa PRcvrable = pa
	var x X = 2
	var x2 X2 = 2
	issue2698 := Issue2698{
		a: 1,
		b: 2,
		c: 3,
		d: 4,
	}

	fn2clos := makeclos(pa)
	fn2glob := call1
	fn2valmeth := pa.VRcvr
	fn2ptrmeth := pa.PRcvr
	var fn2nil func()

	d := &Derived{3, Base{4}}

	runtime.Breakpoint() // breakpoint here
	call1(one, two)
	fn2clos(2)
	strings.LastIndexByte(stringslice[1], 'w')
	d.Method()
	d.Base.Method()
	x.CallMe()
	fmt.Println(one, two, zero, call, call0, call2, callexit, callpanic, callbreak, callstacktrace, stringsJoin, intslice, stringslice, comma, a.VRcvr, a.PRcvr, pa, vable_a, vable_pa, pable_pa, fn2clos, fn2glob, fn2valmeth, fn2ptrmeth, fn2nil, ga, escapeArg, a2, square, intcallpanic, onetwothree, curriedAdd, getAStruct, getAStructPtr, getVRcvrableFromAStruct, getPRcvrableFromAStructPtr, getVRcvrableFromAStructPtr, pa2, noreturncall, str, d, x, x2.CallMe(5), longstrs, regabistacktest, regabistacktest2, issue2698.String())
}
