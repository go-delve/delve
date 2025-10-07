package main

import "fmt"

func main() {
	fmt.Println("start")
	a()
	fmt.Println("end")
}

func a() {
	fmt.Println("in a")
	b()
	fmt.Println("done a")
}

func b() {
	fmt.Println("in b")
	fmt.Println("done b")
}
