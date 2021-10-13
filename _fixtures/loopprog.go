package main

import "fmt"

func loop() {
	i := 0
	for {
		i++
		if (i % 1000000) == 0 {
			fmt.Println(i)
		}
	}
	fmt.Println(i)
}

func main() {
	fmt.Println("past main")
	loop()
}
