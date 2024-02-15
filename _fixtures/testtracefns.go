package main

import "fmt"

func D(i int) int {
	return i * i * i
}
func C(i int) int {

	return D(i+10) + 20
}
func B(i int) int {
	return i * D(i)
}
func A(i int) int {
	d := 10 + B(i)
	return d + C(i)
}
func second(i int) int {
	if i > 0 {
		return first(i - 1)
	} else {
		return 0
	}
}
func first(n int) int {
	if n <= 1 {
		return n
	} else {
		return second(n - 3)
	}
}

func callmed(i int) int {
	return i * i * i
}
func callmee(i int) int {

	return i + 20
}
func callme2(i int) int {
	d := callmee(i) + 40
	return d + callmed(i)
}
func callme(i int) int {
	return 10 + callme2(i)
}

func F0() {
	defer func() {
		recover()
	}()
	F1()
}

func F1() {
	F2()
}

func F2() {
	F3()
}

func F3() {
	F4()
}

func F4() {
	panic("blah")
}

func main() {
	j := 0
	j += A(2)

	j += first(6)
	j += callme(2)
	fmt.Println(j)
	F0()

}
