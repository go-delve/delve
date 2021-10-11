package main

import (
	"os"
	"runtime"
)

func fputestsetup(f64a, f64b, f64c, f64d float64, f32a, f32b, f32c, f32d float32, avx2, avx512, dobreak bool)
func getCPUID70() (ebx, ecx uint32)

func main() {
	var f64a float64 = 1.1
	var f64b float64 = 1.2
	var f64c float64 = 1.3
	var f64d float64 = 1.4
	var f32a float32 = 1.5
	var f32b float32 = 1.6
	var f32c float32 = 1.7
	var f32d float32 = 1.8

	ebx, _ := getCPUID70()
	avx2 := ebx&(1<<5) != 0
	avx512 := ebx&(1<<16) != 0

	fputestsetup(f64a, f64b, f64c, f64d, f32a, f32b, f32c, f32d, avx2, avx512, len(os.Args) < 2 || os.Args[1] != "panic")
	if len(os.Args) < 2 || os.Args[1] != "panic" {
		runtime.Breakpoint()
	} else {
		panic("boom!")
	}
}
