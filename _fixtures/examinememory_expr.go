package main

import "fmt"

func main() {
	var (
		myInt    = 23
		myStr    = "hello"
		mySlice  = []int{3, 4, 5}
		myArray  = [3]int{6, 7, 8}
		myStruct = struct{ a, b int }{9, 10}

		myIntPtr    = &myInt
		myStrPtr    = &myStr
		mySlicePtr  = &mySlice
		myArrayPtr  = &myArray
		myStructPtr = &myStrPtr
	)

	fmt.Sprint(
		myInt,
		myStr,
		mySlice,
		myArray,
		myStruct,

		myIntPtr,
		myStrPtr,
		mySlicePtr,
		myArrayPtr,
		myStructPtr,
	)
}
