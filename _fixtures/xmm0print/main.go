package main

import "fmt"

//go:noescape
func VPSLLQ36(src, dst *[4]uint64)

func main() {
	src := [4]uint64{0: 0x38180a06, 1: 0x38180a06, 2: 0x18080200, 3: 0x18080200}
	dst := [4]uint64{}
	VPSLLQ36(&src, &dst)

	for _, qword := range dst {
		fmt.Printf("%064b\n", qword)
	}
}
