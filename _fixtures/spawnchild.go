package main

import "fmt"

func traceme5() {
	fmt.Println("hello world")
}

func main() {
	for i := 0; i < 3; i++ {
		println(i) // b spawnchild.go:11 if i == 1
	}
	traceme5()
}
