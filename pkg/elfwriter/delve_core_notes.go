package elfwriter

const (
	DelveHeaderNoteType = 0x444C5645 // DLVE
	DelveThreadNodeType = 0x444C5654 // DLVT

	DelveHeaderTargetPidPrefix  = "Target Pid: "
	DelveHeaderEntryPointPrefix = "Entry Point: "
)

//TODO(aarzilli): these constants probably need to be in a better place.
