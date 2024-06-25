package main

import (
	"fmt"
	"iter"
)

func backwards(s []int) func(func(int) bool) {
	return func(yield func(int) bool) {
		for i := len(s) - 1; i >= 0; i-- {
			if !yield(s[i]) {
				break
			}
		}
	}
}

func main() {
	next, stop := iter.Pull(backwards([]int{10, 20, 30, 40}))
	fmt.Println(next())
	fmt.Println(next())
	fmt.Println(next())
	stop()
}
