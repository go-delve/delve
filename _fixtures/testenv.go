package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	x := os.Getenv("VAR1")
	runtime.Breakpoint()
	fmt.Printf("VAR1=%s\n", x)

	ok := false
	x, ok = os.LookupEnv("VAR2")
	if ok {
		runtime.Breakpoint()
		fmt.Printf("VAR2=%s\n", x)
	}
}
