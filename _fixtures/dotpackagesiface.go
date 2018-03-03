package main

import (
	"dir.io"
	"dir.io/io.io"
	"fmt"
	"runtime"
)

func main() {
	var iface interface{} = &dirio.SomeType{}
	var iface2 interface{} = &ioio.SomeOtherType{}
	runtime.Breakpoint()
	fmt.Println(iface, iface2)
}
