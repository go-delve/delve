package dap

// min returns the lowest-valued integer
// between the two passed into it.
func min(i, j int) int {
	if i < j {
		return i
	}
	return j
}
