package main

import "fmt"

func callme() {
	for i := 0; i < 10; i++ {
		callme2()
	}
}

func callme2() {
	fmt.Println("called again!!")
}

func main() {
	callme()
}
