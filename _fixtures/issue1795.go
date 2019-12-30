package main

import (
	"fmt"
	"regexp"
	"runtime"
)

func main() {
	r := regexp.MustCompile("ab")
	runtime.Breakpoint()
	out := r.MatchString("blah")
	fmt.Printf("%v\n", out)
}
