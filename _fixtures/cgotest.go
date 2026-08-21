package main

/*
#include <stdio.h>
char* foo(void) { return "hello, world!"; }
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "sleep" {
		time.Sleep(10 * time.Second)
	}
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println(C.GoString(C.foo()))
}
