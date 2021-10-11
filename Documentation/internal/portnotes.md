# Notes on porting Delve to other architectures

## Continuous Integration requirements

Code that isn't tested doesn't work, we like to run CI on all supported platforms. Currently our CI is done on an [instance of TeamCity cloud provided by JetBrains](https://delve.teamcity.com/), with the exception of the FreeBSD port, which is tested by Cirrus-CI.

TeamCity settings are in `.teamcity/settings.kts` which in turn runs one of `_scripts/test_linux.sh`, `_scripts/test_mac.sh` or `_scripts/test_windows.ps1`.
All test scripts eventually end up calling into our main test program `_scripts/make.go`, which makes the appropriate `go test` calls.

If you plan to port Delve to a new platform you should first figure out how we are going to add your port to our CI, there are three possible solutions:

1. the platform can be run on existing agents we have on TeamCity (linux/amd64, linux/arm64, windows/amd64, darwin/amd64, darwin/arm64) through Docker or similar solutions.
2. there is a free CI service that integrates with GitHub that we can use
3. you provide the hardware to be added to TeamCity to test it

Exception to this requirement can be discussed in special cases.

## General code organization

An introduction to the architecture of Delve can be found in the 2018 Gophercon Iceland talk: [slides](https://speakerdeck.com/aarzilli/internal-architecture-of-delve), [video](https://www.youtube.com/watch?v=IKnTr7Zms1k).

### Packages you shouldn't worry about

* `cmd/dlv/...` implements the command line program
* `pkg/terminal/...` implements the command line user interface
* `service/...` with the exception of `service/test`, implements our API as well as DAP
* `pkg/dwarf/...` with the exception of `pkg/dwarf/regnum`, implements DWARF features not covered by the standard library
* anything else in `pkg` that isn't inside `pkg/proc`

### pkg/proc

`pkg/proc` is the symbolic layer of Delve, its job is to bridge the distance between the API and low level Operating System and CPU features. Almost all features of Delve are implemented here, **except for the interaction with the OS/CPU**, which is provided by one of three backends: `pkg/proc/native`, `pkg/proc/core` or `pkg/proc/gdbserial`.

This package also contains the main test suite for Delve's backends in `pkg/proc/proc_test.go`. The tests for reading variables, however, are inside `service/test` for historical reasons.

When porting Delve to a new CPU a new instance of the `proc.Arch` structure should be filled, see `pkg/proc/arch.go` and `pkg/proc/amd64_arch.go` as an example. To do this you will have to:

- provide a disassembler for the port architecture
- provide a mapping between DWARF register numbers and hardware registers in `pkg/dwarf/regnum` (see `pkg/dwarf/regnum/amd64.go` as an example). This mapping *is not arbitrary* it needs to be described in some standard document which should be linked to in the documentation of `pkg/dwarf/regnum`, for example the mapping for amd64 is described by the System V ABI AMD64 Architecture Processor Supplement v. 1.0 on page 61 figure 3.36.
- if you don't know what `proc.Arch.fixFrameUnwindContext` or `porc.Arch.switchStack` should do *leave them empty*
- the `proc.Arch.prologues` field needs to be filled by looking at the relevant parts of the Go linker (`cmd/link`), the code is somewhere inside `$GOROOT/src/cmd/internal/obj`, usually the function is called `stacksplit`.

If your target OS uses an executable file format other than ELF, Mach-O or PE you will also have to change `pkg/proc/bininfo.go` .

**See also** the note on [build tags](#buildtags).

### pkg/proc/gdbserial

This implements GDB remote serial protocol, it is used as the main backend on macOS as well as connecting to [rr](https://rr-project.org/).

Unless you are making a macOS port you shouldn't worry about this.

### pkg/proc/core

This implements code for reading core files. You don't need to support this for the port platform, see the note on [skippable features](#skippable).
If you decide to do it anyway see the note on [build tags](#buildtags).

### pkg/proc/native

This is the interface between Delve and the OS/CPU, you will definitely want to work on this and it will be the bulk of the port job. The tests for this code however are not in this directory, they are in `pkg/proc` and `service/test`.

## General port process

1. Edit `pkg/proc/native/support_sentinel.go` to disable it in the port platform
2. Fill `proc.Arch` struct for target architecture if it isn't supported already
3. Go in `pkg/proc`
    * run `go test -v`
    * fix compiler errors
    * repeat until compilation succeeds
    * fix test failures
    * repeat until almost all tests pass (see note on [skippable tests and features](#skippable))
5. Go in `service/test`
    * run `go test -v`
    * fix compiler errors
    * repeat until compilation succeeds
    * fix test failures
    * repeat until almost all tests pass (see note on [skippable tests and features](#skippable))
6. Go to the root directory of the project
    * run `go run _scripts/make.go test`
    * fix compiler errors
    * repeat until compilation succeeds
    * fix test failures
    * repeat until almost all tests pass (see note on [skippable tests and features](#skippable))

## Miscellaneous

### <a name='buildtags'> Uses of build tags, runtime.GOOS and runtime.GOARCH

Delve has the ability to read cross-platform core files: you can read a core file of any supported platform with Delve running on any other supported platform.
For example, a core file produced by linux/arm64 can be read using Delve running under windows/amd64.
This feature has far reaching consequences, for example the stack unwinding code in `pkg/proc/stack.go` could be asked to unwind a stack for an architecture different from the one its running under.

What this means in practice is that, in general, using build tags (like `_amd64.go`) or checking `runtime.GOOS` and `runtime.GOARCH` is forbidden throughout Delve's source tree, with two important exceptions:

* `pkg/proc/native` is allowed to check runtime.GOOS/runtime.GOARCH as well as using build tags
* test files are allowed to check runtime.GOOS/runtime.GOARCH as well as using build tags

Other exceptions can be considered, but in general code outside of `pkg/proc/native` should:

* use `proc.BinaryInfo.GOOS` instead of `runtime.GOOS`
* use `proc.BinaryInfo.Arch.Name` instead of `runtime.GOARCH`
* use `proc.BinaryInfo.Arch.PtrSize()` instead of determining the pointer size with `unsafe.Sizeof`
* use `uint64` wherever an address-sized integer is needed, instead of `uintptr`
* use `amd64_filename.go` instead of the build tag version, `filename_amd64.go`

### <a name='skippable'> Features and tests that can be skipped by a port

Delve offers many features, however not all of them are necessary for a useful port of Delve. The following features are optional to implement for a port:

- Reading core files (i.e. `pkg/proc/core`)
- Writing core files (i.e. `pkg/proc/native/dump_*.go`)
- Watchpoints (`(*nativeThread).writeHardwareBreakpoint` etc)
- Supporting CGO calls (`proc.Arch.switchStack`)
- eBPF (`pkg/proc/internal/ebpf`)
- Working with Position Independent Executables (PIE), unless the default buildmode for the port platform is PIE
- Function call injection (`pkg/proc/fncall.go` -- it is probably not supported on the port architecture anyway)

For all these features it is acceptable (and possibly advisable) to either leave the implementation empty or to write a stub that always returns an "not implemented" error. Tests relative to these features can be skipped, `proc_test.go` has a `skipOn` utility function that can be called to skip a specific test on some architectures.

Other tests should pass reliably, it is acceptable to skip some of them as long as most of them will pass.
The following tests should not be skipped even if you will be tempted to:

* `proc.TestNextConcurrent`
* `proc.TestNextConcurrentVariant2`
* `proc.TestBreakpointCounts` (enable `proc.TestBreakpointCountsWithDetection` if you have problems with this)
* `proc.TestStepConcurrentDirect`
* `proc.TestStepConcurrentPtr`

### Porting to Big Endian architectures

Delve was initially written for amd64 and assumed 64bit and little endianness everywhere. The assumption on pointer size has been removed throughout the codebase, the assumption about endianness hasn't. Both `pkg/dwarf/loclist` and `pkg/dwarf/frame` incorrectly assume little endian encoding. Other parts of the code might do the same.

### Porting to ARM32, MIPS and Software Single Stepping

When resuming a thread stopped at a breakpoint Delve will:

1. remove the breakpoint temporarily
2. make the thread execute a single instruction
3. write the breakpoint back

Step 2 is implemented using the hardware single step feature that many CPUs have and that is exposed via PTRACE_SINGLESTEP or similar features.
ARM32 and MIPS do not have a hardware single step implemented, this means that it has to be implemented in software.
The linux kernel used to have a software implementation of PTRACE_SINGLESTEP but those have been removed because they were too burdensome to maintain and delegated the feature to userspace debuggers entirely.

A software singlestep implementation would work like this:

1. decode the current instruction
2. figure out all the possible places where the PC registers could be after executing the current instruction
3. set a breakpoint on all of them
4. resume the thread normally
5. clear all breakpoints created on step 3

Delve does not currently have any infrastructure to help implement this, which means that porting to architectures without hardware singlestep is even more complicated.

