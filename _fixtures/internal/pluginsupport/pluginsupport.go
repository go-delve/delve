package pluginsupport

type Something interface {
	Callback(int) int
}

type SomethingElse interface {
	Callback2(int, int) float64
}
