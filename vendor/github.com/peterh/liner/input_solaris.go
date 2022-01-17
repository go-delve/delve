package liner

import (
	"golang.org/x/sys/unix"
)

const (
	getTermios = unix.TCGETS
	setTermios = unix.TCSETS
)

const (
	icrnl  = unix.ICRNL
	inpck  = unix.INPCK
	istrip = unix.ISTRIP
	ixon   = unix.IXON
	opost  = unix.OPOST
	cs8    = unix.CS8
	isig   = unix.ISIG
	icanon = unix.ICANON
	iexten = unix.IEXTEN
)

type termios unix.Termios

const cursorColumn = false
