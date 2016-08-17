package main

import (
	"os"
	"os/signal"
)

var ch chan os.Signal

func init() {
	ch = make(chan os.Signal)
	signal.Notify(ch, os.Interrupt)
}

func main() {
	<-ch
}
