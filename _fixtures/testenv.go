package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	x := os.Getenv("SOMEVAR")
	runtime.Breakpoint()
	fmt.Printf("SOMEVAR=%s\n", x)
}
