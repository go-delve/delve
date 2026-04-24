// Fixture for issue #3591: nil pointer crash to test SP calculation in core dumps.
package main

func main() {
	map_value := make(map[string]*int)

	var value_1 int = 1
	var value_2 int = 2
	map_value["a"] = &value_1
	map_value["b"] = &value_2

	mod_map(map_value)
}

func mod_map(map_value map[string]*int) {
	p_value := map_value["c"]
	*p_value = 3
}
