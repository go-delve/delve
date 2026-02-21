package main

import "C"

//export GoFunction
func GoFunction() int32 {
	return 42
}

func main() {}
