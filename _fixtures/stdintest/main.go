package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	line, _, err := bufio.NewReader(os.Stdin).ReadLine()

	if err != nil {
		os.Exit(1)
	}

	fmt.Println("echo:", string(line))
}
