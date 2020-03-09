package terminal

type commandGroup uint8

const (
	otherCmds commandGroup = iota
	breakCmds
	runCmds
	dataCmds
	goroutineCmds
	stackCmds
)

type commandGroupDescription struct {
	description string
	group       commandGroup
}

var commandGroupDescriptions = []commandGroupDescription{
	{"Running the program", runCmds},
	{"Manipulating breakpoints", breakCmds},
	{"Viewing program variables and memory", dataCmds},
	{"Listing and switching between threads and goroutines", goroutineCmds},
	{"Viewing the call stack and selecting frames", stackCmds},
	{"Other commands", otherCmds},
}
