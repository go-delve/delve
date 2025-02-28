package main

import (
	"errors"
	"fmt"
)

func main() {
	var err error
	fmt.Println("Error created:", err)
	err = errors.New("modified error")
	fmt.Println("Error modified:", err)
	err = errors.New("final modified error")
	fmt.Println("Error modified final:", err)
}
