package main

import (
	"fmt"
	"os"
)

func main() {
	// this test expects AT LEAST 1 argument, and the first one needs to be "test".
	// second one is optional but if given, it should be "-passFlag"
	fmt.Printf("received args %#v\n", os.Args)
	if len(os.Args) < 2 {
		panic("os.args too short!")
	} else if os.Args[1] != "test" {
		panic("os.args[1] is not test!")
	}
	if len(os.Args) >= 3 && os.Args[2] != "pass flag" {
		panic("os.args[2] is not \"pass flag\"!")
	}
}
