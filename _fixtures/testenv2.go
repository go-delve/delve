package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	x, y := os.LookupEnv("SOMEVAR")
	runtime.Breakpoint()
	fmt.Printf("SOMEVAR=%s\n%v", x, y)
}
