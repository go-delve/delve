package liner

import (
	"golang.org/x/sys/unix"
)

func (s *State) getColumns() bool {
	ws, err := unix.IoctlGetWinsize(unix.Stdout, unix.TIOCGWINSZ)
	if err != nil {
		return false
	}
	s.columns = int(ws.Col)
	return true
}
