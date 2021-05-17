package dap

// Unique identifiers for messages returned for errors from requests.
// These values are not mandated by DAP (other than the uniqueness
// requirement), so each implementation is free to choose their own.
const (
	UnsupportedCommand int = 9999
	InternalError      int = 8888
	NotYetImplemented  int = 7777

	// Where applicable and for consistency only,
	// values below are inspired the original vscode-go debug adaptor.
	FailedToLaunch             = 3000
	FailedToAttach             = 3001
	FailedToInitialize         = 3002
	UnableToSetBreakpoints     = 2002
	UnableToDisplayThreads     = 2003
	UnableToProduceStackTrace  = 2004
	UnableToListLocals         = 2005
	UnableToListArgs           = 2006
	UnableToListGlobals        = 2007
	UnableToLookupVariable     = 2008
	UnableToEvaluateExpression = 2009
	UnableToHalt               = 2010
	UnableToGetExceptionInfo   = 2011
	// Add more codes as we support more requests
	DebuggeeIsRunning = 4000
	DisconnectError   = 5000
)
