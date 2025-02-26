package main

import (
	"errors"
	"fmt"
	"runtime"
)

func main() {
	var err error = errors.New("test error")
	fmt.Println("Error created:", err)
	err = errors.New("modified error")
	fmt.Println("Error modified:", err)
	runtime.Breakpoint()
}
