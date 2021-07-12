package main

import (
	"os"
)

func main() {
	// this test expects two ENV: VAR1=foo VAR2=bar.
	if os.Getenv("VAR1") != "foo" {
		panic("VAR1 is not foo!")
	}
	if os.Getenv("VAR2") != "bar" {
		panic("VAR2 is not bar!")
	}
}
