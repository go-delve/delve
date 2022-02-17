package main

import "fmt"

func f2() {
	fmt.Printf("f2\n")
}

func f1() {
	fmt.Printf("f1\n")
}

func main() {
	f1()
	f2()
}
