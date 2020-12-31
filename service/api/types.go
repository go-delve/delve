package api

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"unicode"

	"github.com/go-delve/delve/pkg/proc"
)

// ErrNotExecutable is an error returned when trying
// to debug a non-executable file.
var ErrNotExecutable = errors.New("not an executable file")

// DebuggerState represents the current context of the debugger.
type DebuggerState struct {
	// Running is true if the process is running and no other information can be collected.
	Running bool
	// Recording is true if the process is currently being recorded and no other
	// information can be collected. While the debugger is in this state
	// sending a StopRecording request will halt the recording, every other
	// request will block until the process has been recorded.
	Recording bool
	// Core dumping currently in progress.
	CoreDumping bool
	// CurrentThread is the currently selected debugger thread.
	CurrentThread *Thread `json:"currentThread,omitempty"`
	// SelectedGoroutine is the currently selected goroutine
	SelectedGoroutine *Goroutine `json:"currentGoroutine,omitempty"`
	// List of all the process threads
	Threads []*Thread
	// NextInProgress indicates that a next or step operation was interrupted by another breakpoint
	// or a manual stop and is waiting to complete.
	// While NextInProgress is set further requests for next or step may be rejected.
	// Either execute continue until NextInProgress is false or call CancelNext
	NextInProgress bool
	// Exited indicates whether the debugged process has exited.
	Exited     bool `json:"exited"`
	ExitStatus int  `json:"exitStatus"`
	// When contains a description of the current position in a recording
	When string
	// Filled by RPCClient.Continue, indicates an error
	Err error `json:"-"`
}

// Breakpoint addresses a set of locations at which process execution may be
// suspended.
type Breakpoint struct {
	// ID is a unique identifier for the breakpoint.
	ID int `json:"id"`
	// User defined name of the breakpoint.
	Name string `json:"name"`
	// Addr is deprecated, use Addrs.
	Addr uint64 `json:"addr"`
	// Addrs is the list of addresses for this breakpoint.
	Addrs []uint64 `json:"addrs"`
	// File is the source file for the breakpoint.
	File string `json:"file"`
	// Line is a line in File for the breakpoint.
	Line int `json:"line"`
	// FunctionName is the name of the function at the current breakpoint, and
	// may not always be available.
	FunctionName string `json:"functionName,omitempty"`

	// Breakpoint condition
	Cond string

	// Tracepoint flag, signifying this is a tracepoint.
	Tracepoint bool `json:"continue"`
	// TraceReturn flag signifying this is a breakpoint set at a return
	// statement in a traced function.
	TraceReturn bool `json:"traceReturn"`
	// retrieve goroutine information
	Goroutine bool `json:"goroutine"`
	// number of stack frames to retrieve
	Stacktrace int `json:"stacktrace"`
	// expressions to evaluate
	Variables []string `json:"variables,omitempty"`
	// LoadArgs requests loading function arguments when the breakpoint is hit
	LoadArgs *LoadConfig
	// LoadLocals requests loading function locals when the breakpoint is hit
	LoadLocals *LoadConfig
	// number of times a breakpoint has been reached in a certain goroutine
	HitCount map[string]uint64 `json:"hitCount"`
	// number of times a breakpoint has been reached
	TotalHitCount uint64 `json:"totalHitCount"`
	// Disabled flag, signifying the state of the breakpoint
	Disabled bool `json:"disabled"`
}

// ValidBreakpointName returns an error if
// the name to be chosen for a breakpoint is invalid.
// The name can not be just a number, and must contain a series
// of letters or numbers.
func ValidBreakpointName(name string) error {
	if _, err := strconv.Atoi(name); err == nil {
		return errors.New("breakpoint name can not be a number")
	}

	for _, ch := range name {
		if !(unicode.IsLetter(ch) || unicode.IsDigit(ch)) {
			return fmt.Errorf("invalid character in breakpoint name '%c'", ch)
		}
	}

	return nil
}

// Thread is a thread within the debugged process.
type Thread struct {
	// ID is a unique identifier for the thread.
	ID int `json:"id"`
	// PC is the current program counter for the thread.
	PC uint64 `json:"pc"`
	// File is the file for the program counter.
	File string `json:"file"`
	// Line is the line number for the program counter.
	Line int `json:"line"`
	// Function is function information at the program counter. May be nil.
	Function *Function `json:"function,omitempty"`

	// ID of the goroutine running on this thread
	GoroutineID int `json:"goroutineID"`

	// Breakpoint this thread is stopped at
	Breakpoint *Breakpoint `json:"breakPoint,omitempty"`
	// Informations requested by the current breakpoint
	BreakpointInfo *BreakpointInfo `json:"breakPointInfo,omitempty"`

	// ReturnValues contains the return values of the function we just stepped out of
	ReturnValues []Variable
	// CallReturn is true if ReturnValues are the return values of an injected call.
	CallReturn bool
}

// Location holds program location information.
// In most cases a Location object will represent a physical location, with
// a single PC address held in the PC field.
// FindLocations however returns logical locations that can either have
// multiple PC addresses each (due to inlining) or no PC address at all.
type Location struct {
	PC       uint64    `json:"pc"`
	File     string    `json:"file"`
	Line     int       `json:"line"`
	Function *Function `json:"function,omitempty"`
	PCs      []uint64  `json:"pcs,omitempty"`
}

// Stackframe describes one frame in a stack trace.
type Stackframe struct {
	Location
	Locals    []Variable
	Arguments []Variable

	FrameOffset        int64
	FramePointerOffset int64

	Defers []Defer

	Bottom bool `json:"Bottom,omitempty"` // Bottom is true if this is the bottom frame of the stack

	Err string
}

// Defer describes a deferred function.
type Defer struct {
	DeferredLoc Location // deferred function
	DeferLoc    Location // location of the defer statement
	SP          uint64   // value of SP when the function was deferred
	Unreadable  string
}

// Var will return the variable described by 'name' within
// this stack frame.
func (frame *Stackframe) Var(name string) *Variable {
	for i := range frame.Locals {
		if frame.Locals[i].Name == name {
			return &frame.Locals[i]
		}
	}
	for i := range frame.Arguments {
		if frame.Arguments[i].Name == name {
			return &frame.Arguments[i]
		}
	}
	return nil
}

// Function represents thread-scoped function information.
type Function struct {
	// Name is the function name.
	Name_  string `json:"name"`
	Value  uint64 `json:"value"`
	Type   byte   `json:"type"`
	GoType uint64 `json:"goType"`
	// Optimized is true if the function was optimized
	Optimized bool `json:"optimized"`
}

// Name will return the function name.
func (fn *Function) Name() string {
	if fn == nil {
		return "???"
	}
	return fn.Name_
}

// VariableFlags is the type of the Flags field of Variable.
type VariableFlags uint16

const (
	// VariableEscaped is set for local variables that escaped to the heap
	//
	// The compiler performs escape analysis on local variables, the variables
	// that may outlive the stack frame are allocated on the heap instead and
	// only the address is recorded on the stack. These variables will be
	// marked with this flag.
	VariableEscaped = 1 << iota

	// VariableShadowed is set for local variables that are shadowed by a
	// variable with the same name in another scope
	VariableShadowed

	// VariableConstant means this variable is a constant value
	VariableConstant

	// VariableArgument means this variable is a function argument
	VariableArgument

	// VariableReturnArgument means this variable is a function return value
	VariableReturnArgument

	// VariableFakeAddress means the address of this variable is either fake
	// (i.e. the variable is partially or completely stored in a CPU register
	// and doesn't have a real address) or possibly no longer availabe (because
	// the variable is the return value of a function call and allocated on a
	// frame that no longer exists)
	VariableFakeAddress

	// VariableCPrt means the variable is a C pointer
	VariableCPtr

	// VariableCPURegister means this variable is a CPU register.
	VariableCPURegister
)

// Variable describes a variable.
type Variable struct {
	// Name of the variable or struct member
	Name string `json:"name"`
	// Address of the variable or struct member
	Addr uint64 `json:"addr"`
	// Only the address field is filled (result of evaluating expressions like &<expr>)
	OnlyAddr bool `json:"onlyAddr"`
	// Go type of the variable
	Type string `json:"type"`
	// Type of the variable after resolving any typedefs
	RealType string `json:"realType"`

	Flags VariableFlags `json:"flags"`

	Kind reflect.Kind `json:"kind"`

	// Strings have their length capped at proc.maxArrayValues, use Len for the real length of a string
	// Function variables will store the name of the function in this field
	Value string `json:"value"`

	// Number of elements in an array or a slice, number of keys for a map, number of struct members for a struct, length of strings
	Len int64 `json:"len"`
	// Cap value for slices
	Cap int64 `json:"cap"`

	// Array and slice elements, member fields of structs, key/value pairs of maps, value of complex numbers
	// The Name field in this slice will always be the empty string except for structs (when it will be the field name) and for complex numbers (when it will be "real" and "imaginary")
	// For maps each map entry will have to items in this slice, even numbered items will represent map keys and odd numbered items will represent their values
	// This field's length is capped at proc.maxArrayValues for slices and arrays and 2*proc.maxArrayValues for maps, in the circumstances where the cap takes effect len(Children) != Len
	// The other length cap applied to this field is related to maximum recursion depth, when the maximum recursion depth is reached this field is left empty, contrary to the previous one this cap also applies to structs (otherwise structs will always have all their member fields returned)
	Children []Variable `json:"children"`

	// Base address of arrays, Base address of the backing array for slices (0 for nil slices)
	// Base address of the backing byte array for strings
	// address of the struct backing chan and map variables
	// address of the function entry point for function variables (0 for nil function pointers)
	Base uint64 `json:"base"`

	// Unreadable addresses will have this field set
	Unreadable string `json:"unreadable"`

	// LocationExpr describes the location expression of this variable's address
	LocationExpr string
	// DeclLine is the line number of this variable's declaration
	DeclLine int64
}

// LoadConfig describes how to load values from target's memory
type LoadConfig struct {
	// FollowPointers requests pointers to be automatically dereferenced.
	FollowPointers bool
	// MaxVariableRecurse is how far to recurse when evaluating nested types.
	MaxVariableRecurse int
	// MaxStringLen is the maximum number of bytes read from a string
	MaxStringLen int
	// MaxArrayValues is the maximum number of elements read from an array, a slice or a map.
	MaxArrayValues int
	// MaxStructFields is the maximum number of fields read from a struct, -1 will read all fields.
	MaxStructFields int
}

// Goroutine represents the information relevant to Delve from the runtime's
// internal G structure.
type Goroutine struct {
	// ID is a unique identifier for the goroutine.
	ID int `json:"id"`
	// Current location of the goroutine
	CurrentLoc Location `json:"currentLoc"`
	// Current location of the goroutine, excluding calls inside runtime
	UserCurrentLoc Location `json:"userCurrentLoc"`
	// Location of the go instruction that started this goroutine
	GoStatementLoc Location `json:"goStatementLoc"`
	// Location of the starting function
	StartLoc Location `json:"startLoc"`
	// ID of the associated thread for running goroutines
	ThreadID   int    `json:"threadID"`
	Status     uint64 `json:"status"`
	WaitSince  int64  `json:"waitSince"`
	WaitReason int64  `json:"waitReason"`
	Unreadable string `json:"unreadable"`
	// Goroutine's pprof labels
	Labels map[string]string `json:"labels,omitempty"`
}

const (
	GoroutineWaiting = proc.Gwaiting
	GoroutineSyscall = proc.Gsyscall
)

// DebuggerCommand is a command which changes the debugger's execution state.
type DebuggerCommand struct {
	// Name is the command to run.
	Name string `json:"name"`
	// ThreadID is used to specify which thread to use with the SwitchThread
	// command.
	ThreadID int `json:"threadID,omitempty"`
	// GoroutineID is used to specify which thread to use with the SwitchGoroutine
	// and Call commands.
	GoroutineID int `json:"goroutineID,omitempty"`
	// When ReturnInfoLoadConfig is not nil it will be used to load the value
	// of any return variables.
	ReturnInfoLoadConfig *LoadConfig
	// Expr is the expression argument for a Call command
	Expr string `json:"expr,omitempty"`

	// UnsafeCall disables parameter escape checking for function calls.
	// Go objects can be allocated on the stack or on the heap. Heap objects
	// can be used by any goroutine; stack objects can only be used by the
	// goroutine that owns the stack they are allocated on and can not surivive
	// the stack frame of allocation.
	// The Go compiler will use escape analysis to determine whether to
	// allocate an object on the stack or the heap.
	// When injecting a function call Delve will check that no address of a
	// stack allocated object is passed to the called function: this ensures
	// the rules for stack objects will not be violated.
	// If you are absolutely sure that the function you are calling will not
	// violate the rules about stack objects you can disable this safety check
	// by setting UnsafeCall to true.
	UnsafeCall bool `json:"unsafeCall,omitempty"`
}

// BreakpointInfo contains informations about the current breakpoint
type BreakpointInfo struct {
	Stacktrace []Stackframe `json:"stacktrace,omitempty"`
	Goroutine  *Goroutine   `json:"goroutine,omitempty"`
	Variables  []Variable   `json:"variables,omitempty"`
	Arguments  []Variable   `json:"arguments,omitempty"`
	Locals     []Variable   `json:"locals,omitempty"`
}

// EvalScope is the scope a command should
// be evaluated in. Describes the goroutine and frame number.
type EvalScope struct {
	GoroutineID  int
	Frame        int
	DeferredCall int // when DeferredCall is n > 0 this eval scope is relative to the n-th deferred call in the current frame
}

const (
	// Continue resumes process execution.
	Continue = "continue"
	// Rewind resumes process execution backwards (target must be a recording).
	Rewind = "rewind"
	// DirecitonCongruentContinue resumes process execution, if a reverse next, step or stepout operation is in progress it will resume execution backward.
	DirectionCongruentContinue = "directionCongruentContinue"
	// Step continues to next source line, entering function calls.
	Step = "step"
	// ReverseStep continues backward to the previous line of source code, entering function calls.
	ReverseStep = "reverseStep"
	// StepOut continues to the return address of the current function
	StepOut = "stepOut"
	// ReverseStepOut continues backward to the calle rof the current function.
	ReverseStepOut = "reverseStepOut"
	// StepInstruction continues for exactly 1 cpu instruction.
	StepInstruction = "stepInstruction"
	// ReverseStepInstruction reverses execution for exactly 1 cpu instruction.
	ReverseStepInstruction = "reverseStepInstruction"
	// Next continues to the next source line, not entering function calls.
	Next = "next"
	// ReverseNext continues backward to the previous line of source code, not entering function calls.
	ReverseNext = "reverseNext"
	// SwitchThread switches the debugger's current thread context.
	SwitchThread = "switchThread"
	// SwitchGoroutine switches the debugger's current thread context to the thread running the specified goroutine
	SwitchGoroutine = "switchGoroutine"
	// Halt suspends the process.
	Halt = "halt"
	// Call resumes process execution injecting a function call.
	Call = "call"
)

// AssemblyFlavour describes the output
// of disassembled code.
type AssemblyFlavour int

const (
	// GNUFlavour will disassemble using GNU assembly syntax.
	GNUFlavour = AssemblyFlavour(proc.GNUFlavour)
	// IntelFlavour will disassemble using Intel assembly syntax.
	IntelFlavour = AssemblyFlavour(proc.IntelFlavour)
	// GoFlavour will disassemble using Go assembly syntax.
	GoFlavour = AssemblyFlavour(proc.GoFlavour)
)

// AsmInstruction represents one assembly instruction at some address
type AsmInstruction struct {
	// Loc is the location of this instruction
	Loc Location
	// Destination of CALL instructions
	DestLoc *Location
	// Text is the formatted representation of the instruction
	Text string
	// Bytes is the instruction as read from memory
	Bytes []byte
	// If Breakpoint is true a breakpoint is set at this instruction
	Breakpoint bool
	// In AtPC is true this is the instruction the current thread is stopped at
	AtPC bool
}

// AsmInstructions is a slice of single instructions.
type AsmInstructions []AsmInstruction

// GetVersionIn is the argument for GetVersion.
type GetVersionIn struct {
}

// GetVersionOut is the result of GetVersion.
type GetVersionOut struct {
	DelveVersion    string
	APIVersion      int
	Backend         string // backend currently in use
	TargetGoVersion string

	MinSupportedVersionOfGo string
	MaxSupportedVersionOfGo string
}

// SetAPIVersionIn is the input for SetAPIVersion.
type SetAPIVersionIn struct {
	APIVersion int
}

// SetAPIVersionOut is the output for SetAPIVersion.
type SetAPIVersionOut struct {
}

// Register holds information on a CPU register.
type Register struct {
	Name        string
	Value       string
	DwarfNumber int
}

// Registers is a list of CPU registers.
type Registers []Register

func (regs Registers) String() string {
	maxlen := 0
	for _, reg := range regs {
		if n := len(reg.Name); n > maxlen {
			maxlen = n
		}
	}

	var buf bytes.Buffer
	for _, reg := range regs {
		fmt.Fprintf(&buf, "%*s = %s\n", maxlen, reg.Name, reg.Value)
	}
	return buf.String()
}

// DiscardedBreakpoint is a breakpoint that is not
// reinstated during a restart.
type DiscardedBreakpoint struct {
	Breakpoint *Breakpoint
	Reason     string
}

// Checkpoint is a point in the program that
// can be returned to in certain execution modes.
type Checkpoint struct {
	ID    int
	When  string
	Where string
}

// Image represents a loaded shared object (go plugin or shared library)
type Image struct {
	Path    string
	Address uint64
}

// Ancestor represents a goroutine ancestor
type Ancestor struct {
	ID    int64
	Stack []Stackframe

	Unreadable string
}

// StacktraceOptions is the type of the Opts field of StacktraceIn that
// configures the stacktrace.
// Tracks proc.StacktraceOptions
type StacktraceOptions uint16

const (
	// StacktraceReadDefers requests a stacktrace decorated with deferred calls
	// for each frame.
	StacktraceReadDefers StacktraceOptions = 1 << iota

	// StacktraceSimple requests a stacktrace where no stack switches will be
	// attempted.
	StacktraceSimple

	// StacktraceG requests a stacktrace starting with the register
	// values saved in the runtime.g structure.
	StacktraceG
)

// ImportPathToDirectoryPath maps an import path to a directory path.
type PackageBuildInfo struct {
	ImportPath    string
	DirectoryPath string
	Files         []string
}

// DumpState describes the state of a core dump in progress
type DumpState struct {
	Dumping bool
	AllDone bool

	ThreadsDone, ThreadsTotal int
	MemDone, MemTotal         uint64

	Err string
}
