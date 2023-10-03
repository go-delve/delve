//go:build !darwin

package macutil

// CheckRosetta returns an error if the calling process is being translated
// by Apple Rosetta.
func CheckRosetta() error {
	return nil
}
