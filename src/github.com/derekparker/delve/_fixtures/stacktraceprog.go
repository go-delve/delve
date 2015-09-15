package main

func stacktraceme() {
	return
}

func func1() {
	stacktraceme()
}

func func2(f func()) {
	f()
}

func main() {
	func1()
	func2(func1)
}
