# Internal Documentation

_This directory holds documentation around the internals of the debugger and how it works._

This is a general overview of the `delve` internal components.

`delve` consists of the debugger engine which can read and understand Go process memory (located
in package `proc`), [dwarf](http://dwarfstd.org/doc/dwarf-2.0.0.pdf) 
[parser](https://golang.org/pkg/debug/dwarf/) and wrapper (go specifics), `terminal` helper,
`service` set of the RPC servers with user friendly `service/debugger` API.

# `proc` package

The `proc` package is the heart of the `delve` program. It implements low-level debugging interface,
remote process and memory manipulation, disasm and etc. `proc` uses `ptrace` on Linux, 
`ptrace` + `mach` specific syscalls on OS X and `WaitForDebugEvent+ContinueDebugEvent` 
`+SuspendThread+ResumeThread` on Windows.

`proc` consists of:

- `Arch` representing CPU architecture
- Breakpoints management
- `Disassemble` disassembles target memory between two points into sequence of `AsmInstructions`,
  with the help of `rsc.io/x86/x86asm`
- `Eval` evaluator of the Go-like expressions (go/ast, go/constant, go/parser, go/printer, go/token)
- `GoVersion` target compiler parser
- `memoryReadWriter` process memory IO
- `loadModuleData` go module parser
- `Process` describes debugging process with mid-level debug API (handling of the remote processes, 
  de/attach, start/stop/stepping, locations, whatever lister, breakpoints managing or return info).
- `ptrace` [syscall](http://tldp.org/LDP/LG/issue81/sandeep.html) go binding used to do low level 
  control of the remote processes, memory IO, start/stop/stepping execution and etc.
- `Registers` interface
- `Stack` iterator over stack frames
- `Thread` management
- `Types` parser
- `Variables` parser

Cross platform debug support is implemented for Linux, Darwin (MacOSX) and Windows. 
Windows lacks support of process attach for now.

## Start

The entry point to the `proc` API is the `Process` struct defined in `proc.go` and looks like:

        p, err := proc.Attach(d.config.AttachPid)
        ... or ...
        p, err := proc.Launch(d.config.ProcessArgs)
        
## `Attach`

This call attaches debugger to the existing process. 

* On Linux it just fallthrough to the `initializeDebugProcess`
* On Darwin it first acquires the mach task first (`acquire_mach_task`), and then goes to `initializeDebugProcess`
* On Windows it uses `DebugActiveProcess` to attach to the existing process first, then it waits for the
  first debug event (CREATE_PROCESS_DEBUG_EVENT) and fallthrough to the `initializeDebugProcess`.

## `Launch`

Launch creates and begins debugging a new process. 

* On Linux it uses `proc.Start`.
* On Darwin it uses a custom fork/exec process `fork_exec` in order to take an advantage of PT_SIGEXC 
  which will turn Unix signals into Mach exceptions. 
* On Windows it uses `CreateProcess+DuplicateHandle`
  
Then control passed to the `initializeDebugProcess`.

## `initializeDebugProcess` completes `Process` struct initialization

This is where actual PTRACE_ATTACH call may be invoked first, if it is attach kind of initialization 
is ongoing (linux+darwin).

Then OS level process info object acquired. Next, the call to the `LoadInformation` parses remote
process executable metadata into `Process` struct, scanning for:

* loadProcessInformation - general process information (/proc/\*/comm, /proc/\*/stat)
* parseDebugFrame - parses ELF `.debug_frame` and `.debug_info` sections
* obtainGoSymbols - parses ELF `.gosymtab`, `.gopclntab` and `.text` sections
* parseDebugLineInfo - parses ELF `.debug_line` section
* loadTypeMap - parses DWARF types map

To finish threads list updated, go compiler version scanned, `G` struct offset set, current `g` loaded,
location of `runtime.startpanic` found and to the most end the breakpoints inited.

## Memory IO

To be written...

## G parser

To be written...

## Breakpoints implementation

To be written...

## `Process.new`

Initialization of the Process struct consists of setting up `ptrace` environment, debugging process
pid, os/arch details and some struct for receiving future info from the victim.

`ptrace` initialization (`handlePtraceFuncs`) uses `runtime.LockOSThread` call to lock current
goroutine to the OS thread due properly setup `ptrace` env. This is due to the fact that ptrace(2) expects
all commands after PTRACE_ATTACH to come from the same thread.

## `ptrace` syscall

_The ptrace system call is crucial to the working of debugger programs like gdb._

ptrace() is a system call that enables one process to control the execution of another. 
It also enables a process to change the core image of another process. The traced process behaves normally 
until a signal is caught. When that occurs the process enters stopped state and informs the tracing 
process by a wait() call. Then tracing process decides how the traced process should respond. 
The only exception is SIGKILL which surely kills the process.

The traced process may also enter the stopped state in response to some specific events during 
its course of execution. This happens only if the tracing process has set any event flags in 
the context of the traced process. The tracing process can even kill the traced one by setting 
the exit code of the traced process. After tracing, the tracer process may kill the traced one 
or leave to continue with its execution.

_Note: Ptrace() is highly dependent on the architecture of the underlying hardware. 
Applications using ptrace are not easily portable across different architectures and implementations._

Links:

* [ptrace(2) - Linux man page](https://linux.die.net/man/2/ptrace)
* [Process Tracing Using Ptrace](http://tldp.org/LDP/LG/issue81/sandeep.html)
* [ptrace - WikiPedia](https://en.wikipedia.org/wiki/Ptrace)

## `ptrace` binding

`ptrace` implementation uses 1 separate goroutine per debugging process (instance of the `ptrace`). To
guarantee safe `ptrace` environment `runtime.LockOSThread` is used. Commands received via Go channel.

## `march`

Mach 3.0 was originally conceived as a simple, extensible, communications microkernel. 
It is capable of running as a stand–alone kernel, with other traditional operating-system 
services such as I/O, file systems, and networking stacks running as user-mode servers.

However, in OS X, Mach is linked with other kernel components into a single kernel address space. 
This is primarily for performance; it is much faster to make a direct call between linked 
components than it is to send messages or do remote procedure calls (RPC) between separate tasks. 

Mach provides:

* object-based APIs with communication channels (for example, ports) as object references
* highly parallel execution, including preemptively scheduled threads and support for SMP
* a flexible scheduling framework, with support for real-time usage
* a complete set of IPC primitives, including messaging, RPC, synchronization, and notification
* support for large virtual address spaces, shared memory regions, and memory objects backed by persistent store
* proven extensibility and portability, for example across instruction set architectures and in distributed environments
* security and resource management as a fundamental principle of design; all resources are virtualized

Links:

* [KernelProgramming/Mach](https://developer.apple.com/library/content/documentation/Darwin/Conceptual/KernelProgramming/Mach/Mach.html)

## Windows debug events

On windows, debugger uses the `WaitForDebugEvent` function at the beginning of its main loop. This function 
blocks the debugger until a debugging event occurs. When the debugging event occurs, the system suspends
all threads in the process being debugged and notifies the debugger of the event.

To debug a process that is already running, the debugger may use `DebugActiveProcess` with the process identifier. 

After the debugger has either created or attached itself to the process it intends to debug, 
the system notifies the debugger of all debugging events that occur in the process, and, 
if specified, in any child processes. For more information about debugging events, see Debugging Events.

The debugger can interact with the user, or manipulate the state of the process being debugged, 
by using the `GetThreadContext`, `GetThreadSelectorEntry`, `ReadProcessMemory`, `SetThreadContext`, 
and `WriteProcessMemory` functions. `GetThreadSelectorEntry` returns the descriptor table entry for a 
specified selector and thread. Debuggers use the descriptor table entry to convert a segment-relative 
address to a linear virtual address. The `ReadProcessMemory` and `WriteProcessMemory` functions require 
linear virtual addresses.

Debuggers frequently read the memory of the process being debugged and write the memory that contains 
instructions to the instruction cache. After the instructions are written, the debugger calls the 
`FlushInstructionCache` function to execute the cached instructions.

The debugger uses the `ContinueDebugEvent` function at the end of its main loop. 
This function allows the process being debugged to continue executing.

Links:

* [Writing the Debugger's Main Loop](https://msdn.microsoft.com/en-us/library/windows/desktop/ms681675(v=vs.85).aspx)
* [WaitForDebugEvent function](https://msdn.microsoft.com/en-us/library/windows/desktop/ms681423(v=vs.85).aspx)
* [Debugging Events](https://msdn.microsoft.com/en-us/library/windows/desktop/ms679302(v=vs.85).aspx)


# `dwarf` 

`dwarf` stands for DWARF Debugging Information Format. The linker emits DWARF3 debugging information 
when writing ELF (Linux, FreeBSD) or Mach-O (Mac OS X) binaries. 
The DWARF code is rich enough to let you do the following:

- load a Go program in GDB version 7.x,
- list all Go, C, and assembly source files by line (parts of the Go runtime are written in C and assembly),
- set breakpoints by line and step through the code,
- print stack traces and inspect stack frames, and
- find the addresses and print the contents of most variables.

Links:

* [DWARF Debugging Information Format](http://dwarfstd.org/doc/Dwarf3.pdf)
* [debug/dwarf](https://golang.org/pkg/debug/dwarf/)
* [Debugging Go code (a status report)](https://blog.golang.org/debugging-go-code-status-report)
* [Debugging Go programs with the GNU Debugger](https://blog.golang.org/debugging-go-programs-with-gnu-debugger)
* [Debugging with GDB](https://golang.org/doc/gdb)

# Server architecture

To be written...

## RPC1

## RPC2

## Debugger interface

# Terminal

To be written...
