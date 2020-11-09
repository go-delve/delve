package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		fmt.Printf("read %q\n", s.Text())
	}
	os.Stdout.Close()
}
