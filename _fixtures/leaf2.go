package main
import "fmt"
func C(i int) int {
        return i*i*i
}
func B(i int) int {
        return C(i)
}
func A(i int) int {
	return i * B(i)
}
func main() {
	j := 0
	j += A(2)
	fmt.Println(j)
}
