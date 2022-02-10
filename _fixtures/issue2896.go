package main

// Vehicle defines the vehicle behavior
type Vehicle interface {
	// Run vehicle can run in a speed
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
	var vehicle Vehicle = &BMWS1000RR{}
	vehicle.Run()
}
