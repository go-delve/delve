package main

// Vehical defines the vehical behavior
type Vehical interface {
	// Run vehical can run in a speed
	Run()
}

// BMWS1000RR defines the motocycle bmw s1000rr
type BMWS1000RR struct {
}

// Run bwm s1000rr run
func (a *BMWS1000RR) Run() {
	println("I can run at 300km/h")
}

func main() {
	var vehical Vehical = &BMWS1000RR{}
	vehical.Run()
}
