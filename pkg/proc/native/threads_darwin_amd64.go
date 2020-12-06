//+build darwin,amd64,macnative

package native

// #include "threads_darwin.h"
// #include "proc_darwin.h"
import "C"

// osSpecificDetails holds information specific to the OSX/Darwin
// operating system / kernel.
type osSpecificDetails struct {
	threadAct C.thread_act_t
	registers C.x86_thread_state64_t
	exists    bool
}
