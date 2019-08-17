package main

import (
	"fmt"
	"runtime"
	"strings"
)

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

func call1(a, b int) int {
	fmt.Printf("first: %d second: %d\n", a, b)
	return a + b
}

func callpanic() {
	fmt.Printf("about to panic\n")
	panic("callpanic panicked")
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

	var vable_a VRcvrable = a
	var vable_pa VRcvrable = pa
	var pable_pa PRcvrable = pa

	fn2clos := makeclos(pa)
	fn2glob := call1
	fn2valmeth := pa.VRcvr
	fn2ptrmeth := pa.PRcvr
	var fn2nil func()

	runtime.Breakpoint()
	call1(one, two)
	fn2clos(2)
	strings.LastIndexByte(stringslice[1], 'w')
	fmt.Println(one, two, zero, callpanic, callstacktrace, stringsJoin, intslice, stringslice, comma, a.VRcvr, a.PRcvr, pa, vable_a, vable_pa, pable_pa, fn2clos, fn2glob, fn2valmeth, fn2ptrmeth, fn2nil, ga, escapeArg, a2, square, intcallpanic, onetwothree, curriedAdd, getAStruct, getAStructPtr, getVRcvrableFromAStruct, getPRcvrableFromAStructPtr, getVRcvrableFromAStructPtr, pa2, noreturncall, str)
}
