package dap

// Unique identifiers for messages returned for errors from requests.
const (
	UnsupportedCommand int = 9999
	InternalError      int = 8888
	NotYetImplemented  int = 7777

	// The values below come from the vscode-go debug adaptor.
	// Although the spec says they should be unique, the adaptor
	// reuses 3000 for launch, attach and program exit failures.
	// TODO(polina): confirm if the extension expects specific ids
	// for specific cases, and we must match the existing adaptor
	// or if these codes can evolve.
	FailedToContinue          = 3000
	UnableToDisplayThreads    = 2003
	UnableToProduceStacktrace = 2004
	// Add more codes as we support more requests
)
