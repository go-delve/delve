//+build darwin,!macnative

package native

import (
	"errors"
	"sync"

	"github.com/go-delve/delve/pkg/proc"
)

var ErrNativeBackendDisabled = errors.New("native backend disabled during compilation")

// Launch returns ErrNativeBackendDisabled.
func Launch(_ []string, _ string, _ bool, _ []string, _ string, _ [3]string) (*proc.Target, error) {
	return nil, ErrNativeBackendDisabled
}

// Attach returns ErrNativeBackendDisabled.
func Attach(_ int, _ []string) (*proc.Target, error) {
	return nil, ErrNativeBackendDisabled
}

// waitStatus is a synonym for the platform-specific WaitStatus
type waitStatus struct{}

// osSpecificDetails holds information specific to the OSX/Darwin
// operating system / kernel.
type osSpecificDetails struct{}

// osProcessDetails holds Darwin specific information.
type osProcessDetails struct{}

func findExecutable(path string, pid int) string {
	panic(ErrNativeBackendDisabled)
}

func killProcess(pid int) error {
	panic(ErrNativeBackendDisabled)
}

func registers(thread *nativeThread) (proc.Registers, error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) loadProcessInformation(wg *sync.WaitGroup) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) requestManualStop() (err error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) resume() error {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) trapWait(pid int) (*nativeThread, error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) stop(trapthread *nativeThread) (err error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) updateThreadList() error {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) kill() (err error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) detach(kill bool) error {
	panic(ErrNativeBackendDisabled)
}

// EntryPoint returns the entry point for the process,
// useful for PIEs.
func (dbp *nativeProcess) EntryPoint() (uint64, error) {
	panic(ErrNativeBackendDisabled)
}

// Blocked returns true if the thread is blocked
func (t *nativeThread) Blocked() bool {
	panic(ErrNativeBackendDisabled)
}

// SetPC sets the value of the PC register.
func (t *nativeThread) SetPC(pc uint64) error {
	panic(ErrNativeBackendDisabled)
}

// SetSP sets the value of the SP register.
func (t *nativeThread) SetSP(sp uint64) error {
	panic(ErrNativeBackendDisabled)
}

// SetDX sets the value of the DX register.
func (t *nativeThread) SetDX(dx uint64) error {
	panic(ErrNativeBackendDisabled)
}

// ReadMemory reads len(buf) bytes at addr into buf.
func (t *nativeThread) ReadMemory(buf []byte, addr uint64) (int, error) {
	panic(ErrNativeBackendDisabled)
}

// WriteMemory writes the contents of data at addr.
func (t *nativeThread) WriteMemory(addr uint64, data []byte) (int, error) {
	panic(ErrNativeBackendDisabled)
}

func (t *nativeThread) resume() error {
	panic(ErrNativeBackendDisabled)
}

func (t *nativeThread) singleStep() error {
	panic(ErrNativeBackendDisabled)
}

func (t *nativeThread) restoreRegisters(sr proc.Registers) error {
	panic(ErrNativeBackendDisabled)
}

// Stopped returns whether the thread is stopped at
// the operating system level.
func (t *nativeThread) Stopped() bool {
	panic(ErrNativeBackendDisabled)
}

func initialize(dbp *nativeProcess) error { return nil }
