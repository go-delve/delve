package main

import (
	"fmt"
	"os"
	"os/signal"
)

func main() {

	fmt.Println("Start")

	sc := make(chan os.Signal, 1)

	//os.Interrupt, os.Kill
	signal.Notify(sc, os.Interrupt, os.Kill)

	quit := <-sc

	fmt.Printf("Receive signal %s \n", quit.String())

	fmt.Println("End")

}
