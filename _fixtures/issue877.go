package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	dyldenv := os.Getenv("DYLD_LIBRARY_PATH")
	runtime.Breakpoint()
	fmt.Println(dyldenv)
}
