package main

func main() {
	ch1 := make(chan string)
	ch2 := make(chan string)
	go func() {
		<-ch1
		ch2 <- "done"
	}()
	<-ch2
	ch1 <- "done"
}
