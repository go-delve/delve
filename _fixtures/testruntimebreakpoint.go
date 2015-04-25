package main

import (
	"fmt"
	"runtime"
)

func main() {
	runtime.Breakpoint()
	fmt.Println("broke")
}
