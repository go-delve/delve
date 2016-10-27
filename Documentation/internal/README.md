# Internal Documentation

_This directory holds documentation around the internals of the debugger and how it works._

This is general overview of the `delve` internal components.

`delve` consists of the debugger engine which can read and understand Go process memory (located
in package `proc`), [dwarf](http://dwarfstd.org/doc/dwarf-2.0.0.pdf) 
[parser](https://golang.org/pkg/debug/dwarf/) and wrapper (go specifics), `terminal` helper,
`service` set RPC servers with user (RPC) friendly `service/debugger` wrapper.

# `proc` package

The `proc` package is the heart of the `delve` program. It implements low-level debugging interface,
remote process and memory manipulation, disasm and etc.

`proc` consists of:

- `Arch` representing CPU architecture
- Breakpoints
- `Disassemble` which disassembles target memory between two points into sequence of `AsmInstructions`,
  with the help of "rsc.io/x86/x86asm"
- `Eval` evaluator of the Go-like expressions (go/ast, go/constant, go/parser, go/printer, go/token)
- `GoVersion` target compiler parser
- `memoryReadWriter` and implementations
- `loadModuleData` which can parse go modules
- `Process` main struct describe debugging process with mid level debug API (handle remote process, de/attach, 
  start/stop/step, locate, list whatever, manage breakpoints or return info).
- `ptrace` [syscall](http://tldp.org/LDP/LG/issue81/sandeep.html) go binding used to do low level 
  control of remote processes, read write memory, start stop or continue execution and etc.
- `Registers` interface
- `Stack` iterator with `Stackframes`
- `Thread`s managing
- `Types` parser
- `Variables` parser

A lot of debug information is available out of `dwarf` and sometimes `disassembly`. Cross platform 
debug support is implemented for linux, darwin (macosx) and windows. Windows lacks support of process
attach for now.

The entry point to the `proc` API is the `Process` struct defined in `proc.go` and looks like:

        p, err := proc.Attach(d.config.AttachPid)
        ... or ...
        p, err := proc.Launch(d.config.ProcessArgs)

# `ptrace` syscall

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

Note: Ptrace() is highly dependent on the architecture of the underlying hardware. 
Applications using ptrace are not easily portable across different architectures and implementations.

* [ptrace(2) - Linux man page](https://linux.die.net/man/2/ptrace)
* [Process Tracing Using Ptrace](http://tldp.org/LDP/LG/issue81/sandeep.html)
* [ptrace - WikiPedia](https://en.wikipedia.org/wiki/Ptrace)

# `dwarf` 

`dwarf` stands for DWARF Debugging Information Format. The 6l and 8l linkers emit DWARF3 debugging information 
when writing ELF (Linux, FreeBSD) or Mach-O (Mac OS X) binaries. 
The DWARF code is rich enough to let you do the following:

- load a Go program in GDB version 7.x,
- list all Go, C, and assembly source files by line (parts of the Go runtime are written in C and assembly),
- set breakpoints by line and step through the code,
- print stack traces and inspect stack frames, and
- find the addresses and print the contents of most variables.

* [DWARF Debugging Information Format](http://dwarfstd.org/doc/dwarf-2.0.0.pdf)
* [debug/dwarf](https://golang.org/pkg/debug/dwarf/)
* [Debugging Go code (a status report)](https://blog.golang.org/debugging-go-code-status-report)
* [Debugging Go programs with the GNU Debugger](https://blog.golang.org/debugging-go-programs-with-gnu-debugger)
* [Debugging with GDB](https://golang.org/doc/gdb)
