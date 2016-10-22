package main

import (
	"fmt"
	"math/rand"
	"runtime"
)

func main() {
	runtime.Breakpoint()
	fmt.Println(rand.Intn(10))
}
