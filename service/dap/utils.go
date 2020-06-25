package dap

type tuple struct {
	a, b interface{}
}

var min = func(i, j int) int {
	if i < j {
		return i
	}
	return j
}
