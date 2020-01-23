package main

import (
	"fmt"
	"time"
)

func main() {
	sum := int64(0)
	start := time.Now()
	for value := int64(0); value < 10000; value++ {
		sum += value
	}
	elapsed := time.Since(start)
	fmt.Printf("Sum: %d\nTook %s\n", sum, elapsed)
}
