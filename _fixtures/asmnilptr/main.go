package main

import "fmt"

func asmFunc(*int) int

func main() {
	fmt.Printf("%d\n", asmFunc(nil))
}
