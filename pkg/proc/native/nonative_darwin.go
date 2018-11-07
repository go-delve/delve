//+build darwin,!macnative

package native

import (
	"errors"
	"sync"

	"github.com/derekparker/delve/pkg/proc"
)

var ErrNativeBackendDisabled = errors.New("native backend disabled during compilation")

// Launch returns ErrNativeBackendDisabled.
func Launch(cmd []string, wd string, foreground bool, _ []string) (*Process, error) {
	return nil, ErrNativeBackendDisabled
}

// Attach returns ErrNativeBackendDisabled.
func Attach(pid int, _ []string) (*Process, error) {
	return nil, ErrNativeBackendDisabled
}

// WaitStatus is a synonym for the platform-specific WaitStatus
type WaitStatus struct{}

// OSSpecificDetails holds information specific to the OSX/Darwin
// operating system / kernel.
type OSSpecificDetails struct{}

// OSProcessDetails holds Darwin specific information.
type OSProcessDetails struct{}

func findExecutable(path string, pid int) string {
	panic(ErrNativeBackendDisabled)
}

func killProcess(pid int) error {
	panic(ErrNativeBackendDisabled)
}

func registers(thread *Thread, floatingPoint bool) (proc.Registers, error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) loadProcessInformation(wg *sync.WaitGroup) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) requestManualStop() (err error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) resume() error {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) trapWait(pid int) (*Thread, error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) stop(trapthread *Thread) (err error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) updateThreadList() error {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) kill() (err error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) detach(kill bool) error {
	panic(ErrNativeBackendDisabled)
}

func (dbp *Process) entryPoint() (uint64, error) {
	panic(ErrNativeBackendDisabled)
}

// Blocked returns true if the thread is blocked
func (t *Thread) Blocked() bool {
	panic(ErrNativeBackendDisabled)
}

// SetPC sets the value of the PC register.
func (thread *Thread) SetPC(pc uint64) error {
	panic(ErrNativeBackendDisabled)
}

// SetSP sets the value of the SP register.
func (thread *Thread) SetSP(sp uint64) error {
	panic(ErrNativeBackendDisabled)
}

// SetDX sets the value of the DX register.
func (thread *Thread) SetDX(dx uint64) error {
	panic(ErrNativeBackendDisabled)
}

// ReadMemory reads len(buf) bytes at addr into buf.
func (t *Thread) ReadMemory(buf []byte, addr uintptr) (int, error) {
	panic(ErrNativeBackendDisabled)
}

// WriteMemory writes the contents of data at addr.
func (t *Thread) WriteMemory(addr uintptr, data []byte) (int, error) {
	panic(ErrNativeBackendDisabled)
}

func (t *Thread) resume() error {
	panic(ErrNativeBackendDisabled)
}

func (t *Thread) singleStep() error {
	panic(ErrNativeBackendDisabled)
}

func (t *Thread) restoreRegisters(sr proc.Registers) error {
	panic(ErrNativeBackendDisabled)
}

// Stopped returns whether the thread is stopped at
// the operating system level.
func (t *Thread) Stopped() bool {
	panic(ErrNativeBackendDisabled)
}
