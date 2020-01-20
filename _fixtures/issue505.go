package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	file, err := exec.LookPath(os.Args[0])
	fmt.Printf("%s - %s", file, err)
}
