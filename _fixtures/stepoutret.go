package main

import "fmt"

func stepout(n int) (str string, num int) {
	return fmt.Sprintf("return %d", n), n + 1
}

func main() {
	stepout(47)
}
