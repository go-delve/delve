package main

import "runtime"

func main() {
	type StructWithInterface struct {
		Val any
	}
	var i int
	var ba = StructWithInterface{}
	ba.Val = &i

	var ptrPtr **int
	_ = ptrPtr

	runtime.Breakpoint()
}
