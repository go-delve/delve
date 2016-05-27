package types

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"unicode"

	"github.com/derekparker/delve/proc"
)

// DebuggerState represents the current context of the debugger.
type DebuggerState struct {
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
	// Filled by RPCClient.Continue, indicates an error
	Err error `json:"-"`
}

// Breakpoint addresses a location at which process execution may be
// suspended.
type Breakpoint struct {
	// ID is a unique identifier for the breakpoint.
	ID int `json:"id"`
	// User defined name of the breakpoint
	Name string `json:"name"`
	// Addr is the address of the breakpoint.
	Addr uint64 `json:"addr"`
	// File is the source file for the breakpoint.
	File string `json:"file"`
	// Line is a line in File for the breakpoint.
	Line int `json:"line"`
	// FunctionName is the name of the function at the current breakpoint, and
	// may not always be available.
	FunctionName string `json:"functionName,omitempty"`

	// Breakpoint condition
	Cond string

	// tracepoint flag
	Tracepoint bool `json:"continue"`
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
}

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
	BreakpointInfo *BreakpointInfo `json:"breakPointInfo,omitrempty"`
}

type Location struct {
	PC       uint64    `json:"pc"`
	File     string    `json:"file"`
	Line     int       `json:"line"`
	Function *Function `json:"function,omitempty"`
}

type Stackframe struct {
	Location
	Locals    []Variable
	Arguments []Variable
}

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
	Name   string `json:"name"`
	Value  uint64 `json:"value"`
	Type   byte   `json:"type"`
	GoType uint64 `json:"goType"`
}

// Variable describes a variable.
type Variable struct {
	// Name of the variable or struct member
	Name string `json:"name"`
	// Address of the variable or struct member
	Addr uintptr `json:"addr"`
	// Only the address field is filled (result of evaluating expressions like &<expr>)
	OnlyAddr bool `json:"onlyAddr"`
	// Go type of the variable
	Type string `json:"type"`
	// Type of the variable after resolving any typedefs
	RealType string `json:"realType"`

	Kind reflect.Kind `json:"kind"`

	//Strings have their length capped at proc.maxArrayValues, use Len for the real length of a string
	//Function variables will store the name of the function in this field
	Value string `json:"value"`

	// Number of elements in an array or a slice, number of keys for a map, number of struct members for a struct, length of strings
	Len int64 `json:"len"`
	// Cap value for slices
	Cap int64 `json:"cap"`

	// Array and slice elements, member fields of structs, key/value pairs of maps, value of complex numbers
	// The Name field in this slice will always be the empty string except for structs (when it will be the field name) and for complex numbers (when it will be "real" and "imaginary")
	// For maps each map entry will have to items in this slice, even numbered items will represent map keys and odd numbered items will represent their values
	// This field's length is capped at proc.maxArrayValues for slices and arrays and 2*proc.maxArrayValues for maps, in the circumnstances where the cap takes effect len(Children) != Len
	// The other length cap applied to this field is related to maximum recursion depth, when the maximum recursion depth is reached this field is left empty, contrary to the previous one this cap also applies to structs (otherwise structs will always have all their member fields returned)
	Children []Variable `json:"children"`

	// Unreadable addresses will have this field set
	Unreadable string `json:"unreadable"`
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
}

// DebuggerCommand is a command which changes the debugger's execution state.
type DebuggerCommand struct {
	// Name is the command to run.
	Name string `json:"name"`
	// ThreadID is used to specify which thread to use with the SwitchThread
	// command.
	ThreadID int `json:"threadID,omitempty"`
	// GoroutineID is used to specify which thread to use with the SwitchGoroutine
	// command.
	GoroutineID int `json:"goroutineID,omitempty"`
}

// Informations about the current breakpoint
type BreakpointInfo struct {
	Stacktrace []Stackframe `json:"stacktrace,omitempty"`
	Goroutine  *Goroutine   `json:"goroutine,omitempty"`
	Variables  []Variable   `json:"variables,omitempty"`
	Arguments  []Variable   `json:"arguments,omitempty"`
	Locals     []Variable   `json:"locals,omitempty"`
}

type EvalScope struct {
	GoroutineID int
	Frame       int
}

const (
	// Continue resumes process execution.
	Continue = "continue"
	// Step continues to next source line, entering function calls.
	Step = "step"
	// SingleStep continues for exactly 1 cpu instruction.
	StepInstruction = "stepInstruction"
	// Next continues to the next source line, not entering function calls.
	Next = "next"
	// SwitchThread switches the debugger's current thread context.
	SwitchThread = "switchThread"
	// SwitchGoroutine switches the debugger's current thread context to the thread running the specified goroutine
	SwitchGoroutine = "switchGoroutine"
	// Halt suspends the process.
	Halt = "halt"
)

type AssemblyFlavour int

const (
	GNUFlavour   = AssemblyFlavour(proc.GNUFlavour)
	IntelFlavour = AssemblyFlavour(proc.IntelFlavour)
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

type AsmInstructions []AsmInstruction
