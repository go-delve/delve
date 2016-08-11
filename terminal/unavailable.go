package terminal

type unavailable string

func (s unavailable) Error() string {
	return string(s) + ": unavailable"
}
