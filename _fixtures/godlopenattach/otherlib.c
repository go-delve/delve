// A C shared library that is neither linked by the host program nor loaded
// by the Go runtime, so that dlopen'ing it after the debugger has attached
// always produces a new shared library load event.
int OtherFunction(void) { return 42; }
