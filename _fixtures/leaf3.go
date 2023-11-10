package main
import "fmt"
func C(i int) int {
        return i*i*i
}
func B(i int) int {
        return i+20
}
func A(i int) int {
	d:= i * B(i)
	return d*C(i)
}
func main() {
	j := 0
	j += A(2)
	fmt.Println(j)
}
