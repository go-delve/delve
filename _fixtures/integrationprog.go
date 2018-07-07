package main

import (
	"fmt"
	"time"
)

func sayhi(i int) string {
	return fmt.Sprintf("hi %d", i)
}

func main() {
	time.Sleep(1 * time.Second)
	for i := 0; i < 3; i++ {
		ret := sayhi(i)
		fmt.Println(ret)
		time.Sleep(1 * time.Second)
	}
}
