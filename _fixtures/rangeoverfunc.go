package main

// The tests here are adapted from $GOROOT/src/cmd/compile/internal/rangefunc/rangefunc_test.go

import (
	"fmt"
)

/*



THESE
LINES
INTENTIONALLY
LEFT
BLANK




*/

func TestTrickyIterAll() {
	trickItAll := TrickyIterator{}
	i := 0
	for _, x := range trickItAll.iterAll([]int{30, 7, 8, 9, 10}) {
		i += x
		if i >= 36 {
			break
		}
	}

	fmt.Println("Got i = ", i)
}

func TestTrickyIterAll2() {
	trickItAll := TrickyIterator{}
	i := 0
	for _, x := range trickItAll.iterAll([]int{42, 47}) {
		i += x
	}
	fmt.Println(i)
}

func TestBreak1() {
	var result []int
	for _, x := range OfSliceIndex([]int{-1, -2, -4}) {
		if x == -4 {
			break
		}
		for _, y := range OfSliceIndex([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
			if y == 3 {
				break
			}
			result = append(result, y)
		}
		result = append(result, x)
	}
	fmt.Println(result)
}

func TestBreak2() {
	var result []int
outer:
	for _, x := range OfSliceIndex([]int{-1, -2, -4}) {
		for _, y := range OfSliceIndex([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
			if y == 3 {
				break
			}
			if x == -4 {
				break outer
			}
			result = append(result, y)
		}
		result = append(result, x)
	}
	fmt.Println(result)
}

func TestMultiCont0() {
	var result []int = make([]int, 0, 10)

W:
	for _, w := range OfSliceIndex([]int{1000, 2000}) {
		result = append(result, w)
		if w == 2000 {
			break
		}
		for _, x := range OfSliceIndex([]int{100, 200, 300, 400}) {
			for _, y := range OfSliceIndex([]int{10, 20, 30, 40}) {
				result = append(result, y)
				for _, z := range OfSliceIndex([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
					if z&1 == 1 {
						continue
					}
					result = append(result, z)
					if z >= 4 {
						continue W // modified to be multilevel
					}
				}
				result = append(result, -y) // should never be executed
			}
			result = append(result, x)
		}
	}
	fmt.Println(result)
}

func TestPanickyIterator1() {
	var result []int
	defer func() {
		r := recover()
		fmt.Println("Recovering", r)
	}()
	for _, z := range PanickyOfSliceIndex([]int{1, 2, 3, 4}) {
		result = append(result, z)
		if z == 4 {
			break
		}
	}
	fmt.Println(result)
}

func TestPanickyIterator2() {
	var result []int
	defer func() {
		r := recover()
		fmt.Println("Recovering ", r)
	}()
	for _, x := range OfSliceIndex([]int{100, 200}) {
		result = append(result, x)
	Y:
		// swallows panics and iterates to end BUT `break Y` disables the body, so--> 10, 1, 2
		for _, y := range VeryBadOfSliceIndex([]int{10, 20}) {
			result = append(result, y)

			// converts early exit into a panic --> 1, 2
			for k, z := range PanickyOfSliceIndex([]int{1, 2}) { // iterator panics
				result = append(result, z)
				if k == 1 {
					break Y
				}
			}
		}
	}
}

func TestPanickyIteratorWithNewDefer() {
	var result []int
	defer func() {
		r := recover()
		fmt.Println("Recovering ", r)
	}()
	for _, x := range OfSliceIndex([]int{100, 200}) {
		result = append(result, x)
	Y:
		// swallows panics and iterates to end BUT `break Y` disables the body, so--> 10, 1, 2
		for _, y := range VeryBadOfSliceIndex([]int{10, 20}) {
			defer func() { // This defer will be set on TestPanickyIteratorWithNewDefer from TestPanickyIteratorWithNewDefer-range2
				fmt.Println("y loop defer")
			}()
			result = append(result, y)

			// converts early exit into a panic --> 1, 2
			for k, z := range PanickyOfSliceIndex([]int{1, 2}) { // iterator panics
				result = append(result, z)
				if k == 1 {
					break Y
				}
			}
		}
	}
}

func TestLongReturnWrapper() {
	TestLongReturn()
	fmt.Println("returned")
}

func TestLongReturn() {
	for _, x := range OfSliceIndex([]int{1, 2, 3}) {
		for _, y := range OfSliceIndex([]int{10, 20, 30}) {
			if y == 10 {
				return
			}
		}
		fmt.Println(x)
	}
}

func TestGotoA1() {
	result := []int{}
	for _, x := range OfSliceIndex([]int{-1, -4, -5}) {
		result = append(result, x)
		if x == -4 {
			break
		}
		for _, y := range OfSliceIndex([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
			if y == 3 {
				goto A
			}
			result = append(result, y)
		}
	A:
		result = append(result, x)
	}
	fmt.Println(result)
}

func TestGotoB1() {
	result := []int{}
	for _, x := range OfSliceIndex([]int{-1, -2, -3, -4, -5}) {
		result = append(result, x)
		if x == -4 {
			break
		}
		for _, y := range OfSliceIndex([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
			if y == 3 {
				goto B
			}
			result = append(result, y)
		}
		result = append(result, x)
	}
B:
	result = append(result, 999)
	fmt.Println(result)
}

func TestRecur(n int) {
	result := []int{}
	if n > 0 {
		TestRecur(n - 1)
	}
	for _, x := range OfSliceIndex([]int{10, 20, 30}) {
		result = append(result, x)
		if n == 3 {
			TestRecur(0)
		}
	}
	fmt.Println(result)
}

func main() {
	TestTrickyIterAll()
	TestTrickyIterAll2()
	TestBreak1()
	TestBreak2()
	TestMultiCont0()
	TestPanickyIterator1()
	TestPanickyIterator2()
	TestPanickyIteratorWithNewDefer()
	TestLongReturnWrapper()
	TestGotoA1()
	TestGotoB1()
	TestRecur(3)
}

type Seq[T any] func(yield func(T) bool)
type Seq2[T1, T2 any] func(yield func(T1, T2) bool)

type TrickyIterator struct {
	yield func()
}

func (ti *TrickyIterator) iterAll(s []int) Seq2[int, int] {
	return func(yield func(int, int) bool) {
		// NOTE: storing the yield func in the iterator has been removed because
		// it make the closure escape to the heap which breaks the .closureptr
		// heuristic. Eventually we will need to figure out what to do when that
		// happens.
		// ti.yield = yield // Save yield for future abuse
		for i, v := range s {
			if !yield(i, v) {
				return
			}
		}
		return
	}
}

// OfSliceIndex returns a Seq2 over the elements of s. It is equivalent
// to range s.
func OfSliceIndex[T any, S ~[]T](s S) Seq2[int, T] {
	return func(yield func(int, T) bool) {
		for i, v := range s {
			if !yield(i, v) {
				return
			}
		}
		return
	}
}

// PanickyOfSliceIndex iterates the slice but panics if it exits the loop early
func PanickyOfSliceIndex[T any, S ~[]T](s S) Seq2[int, T] {
	return func(yield func(int, T) bool) {
		for i, v := range s {
			if !yield(i, v) {
				panic(fmt.Errorf("Panicky iterator panicking"))
			}
		}
		return
	}
}

// VeryBadOfSliceIndex is "very bad" because it ignores the return value from yield
// and just keeps on iterating, and also wraps that call in a defer-recover so it can
// keep on trying after the first panic.
func VeryBadOfSliceIndex[T any, S ~[]T](s S) Seq2[int, T] {
	return func(yield func(int, T) bool) {
		for i, v := range s {
			func() {
				defer func() {
					recover()
				}()
				yield(i, v)
			}()
		}
		return
	}
}
