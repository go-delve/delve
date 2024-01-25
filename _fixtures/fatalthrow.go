package main

func main() {
	// fatal error: concurrent map read and map write
	m := map[string]string{}
	key := "key"

	go func() {
		for {
			m[key] = "a"
		}
	}()

	for {
		_ = m[key]
	}
}
