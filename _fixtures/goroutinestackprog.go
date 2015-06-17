package main

const N = 10

func agoroutine(done chan<- struct{}) {
	done <- struct{}{}
}

func stacktraceme() {
	return
}

func main() {
	done := make(chan struct{})
	for i := 0; i < N; i++ {
		go agoroutine(done)
	}
	stacktraceme()
	for i := 0; i < N; i++ {
		<-done
	}
}
