package main

import "fmt"

func breakfunc1() {
	fmt.Println("breakfunc1")
}

func breakfunc2() {
	fmt.Println("breakfunc2")
}

func breakfunc3() {
	fmt.Println("breakfunc3")
}

func main() {
	breakfunc2()
	breakfunc3()

	breakfunc1() // hit
	breakfunc3()
	breakfunc1()

	breakfunc2() // hit
	breakfunc1()

	breakfunc3() // hit
	breakfunc1()
	breakfunc2()
}
