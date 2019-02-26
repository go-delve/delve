package main

import "time"

func main() {
	for i := 0; i < 5; i++ {
		go func() {
			time.Sleep(11 * time.Millisecond)
		}()
	}

	x := 255
	println(x) //break
}
