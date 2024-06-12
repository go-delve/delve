package main

import (
	"fmt"
)

func multiRound() {
	vars := []int{0, 1, 2, 3, 4, 5}
	for i := range vars { // line 9: set watchpoints
		if i > 0 {
			vars[i] = vars[i-1]
			fmt.Println() // line 12: watchpoints hit
		}
	}
}

func main() {
	multiRound() // line 18: after restart, last watchpoint out of scope
	return       // line 19: all watchpoints should go out of scope
}
