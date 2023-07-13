//go:build darwin && !macnative
// +build darwin,!macnative

package native

import (
	"errors"
	"sync"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"
)

var ErrNativeBackendDisabled = errors.New("native backend disabled during compilation")

// Launch returns ErrNativeBackendDisabled.
func Launch(_ []string, _ string, _ proc.LaunchFlags, _ []string, _ string, _ string, _ proc.OutputRedirect, _ proc.OutputRedirect) (*proc.TargetGroup, error) {
	return nil, ErrNativeBackendDisabled
}

// Attach returns ErrNativeBackendDisabled.
func Attach(_ int, _ *proc.WaitFor, _ []string) (*proc.TargetGroup, error) {
	return nil, ErrNativeBackendDisabled
}

func waitForSearchProcess(string, map[int]struct{}) (int, error) {
	return 0, proc.ErrWaitForNotImplemented
}

// waitStatus is a synonym for the platform-specific WaitStatus
type waitStatus struct{}

// osSpecificDetails holds information specific to the OSX/Darwin
// operating system / kernel.
type osSpecificDetails struct{}

// osProcessDetails holds Darwin specific information.
type osProcessDetails struct{}

func (os *osProcessDetails) Close() {}

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

func (*processGroup) resume() error {
	panic(ErrNativeBackendDisabled)
}

func trapWait(procgrp *processGroup, pid int) (*nativeThread, error) {
	panic(ErrNativeBackendDisabled)
}

func (*processGroup) stop(cctx *proc.ContinueOnceContext, trapthread *nativeThread) (*nativeThread, error) {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) updateThreadList() error {
	panic(ErrNativeBackendDisabled)
}

func (*processGroup) kill(dbp *nativeProcess) (err error) {
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

func (dbp *nativeProcess) SupportsBPF() bool {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) SetUProbe(fnName string, goidOffset int64, args []ebpf.UProbeArgMap) error {
	panic(ErrNativeBackendDisabled)
}

func (dbp *nativeProcess) GetBufferedTracepoints() []ebpf.RawUProbeParams {
	panic(ErrNativeBackendDisabled)
}

// SetPC sets the value of the PC register.
func (t *nativeThread) setPC(pc uint64) error {
	panic(ErrNativeBackendDisabled)
}

// SetReg changes the value of the specified register.
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
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

func (*processGroup) singleStep(*nativeThread) error {
	panic(ErrNativeBackendDisabled)
}

func (t *nativeThread) restoreRegisters(sr proc.Registers) error {
	panic(ErrNativeBackendDisabled)
}

func (t *nativeThread) withDebugRegisters(f func(*amd64util.DebugRegisters) error) error {
	return proc.ErrHWBreakUnsupported
}

// Stopped returns whether the thread is stopped at
// the operating system level.
func (t *nativeThread) Stopped() bool {
	panic(ErrNativeBackendDisabled)
}

// SoftExc returns true if this thread received a software exception during the last resume.
func (t *nativeThread) SoftExc() bool {
	panic(ErrNativeBackendDisabled)
}

func initialize(dbp *nativeProcess) (string, error) { return "", nil }
