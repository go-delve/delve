# Changelog

All notable changes to this project will be documented in this file.
This project adheres to Semantic Versioning.

## [1.21.1] 2023-10-3

### Added

- Support for linux/ppc64le (@alexsaezm)
- Enable debugging stripped mach-o (MacOS) binaries (@pgavlin)
- Enable function call injection for linux/ppc64le (@archanaravindar)
- Improved support for editors in 'edit' command (@derekparker)
- Early 1.22 support (@aarzilli)
- Add 'packages' command to list packages in compiled binary (@hyangah)
- Support debugging PIE binaries on linux/ppc64le (@archanaravindar)
- Add ability to list goroutines waiting on channel (@aarzilli)
- Add support for more argument / return type parsing in ebpf tracing backend (@derekparker)
- Add waitfor option to 'attach' command (@aarzilli)

### Fixed

- Fix memory leak in native Darwin backend CGO usage (@thechampagne)
- Fix hexadecimal printing of variables with symbolic const values (@aarzilli)
- Handle ctrl-c during tracing executing (@derekparker)
- Use a stack for restore/remember opcodes in DWARF CFI (@javierhonduco)
- Pass user specified '-C' argument first in gobuild flags (@aarzilli)
- Fix stacktraces on freebsd/amd64/go1.20 (@aarzilli)
- Fix PIE support on macOS (@aarzilli)
- Fix starlark slice unmarshaling (@andreimatei)
- Restore breakpoints set with line offsets on restart (@aarzilli)
- Check recursion level when printing pointers (@aarzilli)
- Fix FrameDescriptionEntrie's append bug removing duplicates (@gocurr)
- Read context from sigtrampgo fixing cgo stack traces on 1.21 (@aarzilli)

### Changed

- Replace deprecated io/ioutil usage (@alexandear)
- Accept string list as launch requests buildFlags in DAP server (@hyangah)
- Strip package paths from symbols in callstack in DAP response (@stefanhaller)
- Update cilium/ebpf package (@aarzilli)

## [1.21.0] 2023-06-23

### Added

- Go 1.21 support (#3370, #3401, @aarzilli)
- Basic debug functionality for stripped executables (#3408, #3421, @derekparker)
- Core dumping on FreeBSD (#3305, @aarzilli)
- Ability to automatically attach to child processes when they are spawned (linux-only) (#3286, #3346, @aarzilli)
- Starlark scripts can now use the time module (#3375, @andreimatei)
- Online help for starlark interpreter (#3388, @aarzilli)
- `dlv trace` has a new option, `--timestamp`, that prints timestamps before every trace event (#3358, @spacewander)

### Fixed

- Stepping could hide normal breakpoint hits occurring simultaneously, in rare circumstances (#3287, @aarzilli)
- Internal error when defer locations can not be resolved (#3329, @aarzilli)
- Channels of certain types could be displayed incorrectly (#3362, @aarzilli)
- Formatted time wouldn't always be displayed when using DAP (#3349, @suzmue)
- Function call injection when parameters are in registers (#3365, @ZekeLu)
- Panic in Starlark interface when using partially loaded variables (#3386, @andreimatei)
- Use debug info directories configuration in `dlv trace` (#3405, @nozzy123nozzy)
- Updated DAP version (#3414, @suzmue)

### Changed

- Improved eBPF tracepoints (#3325, #3353, #3417, @chenhengqi, @derekparker)
- The name of the default output binary is now randomizes, which fixes some weird behavior when multiple instances of Delve are run simultaneously in the same directory (#3366, @aarzilli)
- Stderr of debuginfod-find is now suppressed (#3381, @fche)
- Better documentation for substitute-path (#3335, @aarzilli)
- `quit -c` will ask for confirmation when breakpoints still exist (#3398, @aarzilli)
- Miscellaneous improvements to documentation and tests (#3326, #3327, #3340, #3341, #3357, #3378, #3376, #3399, #3374, #3426, @alexandear, @cuishuang, @alexsaezm, @andreimatei)


## [1.20.2] 2023-04-05

### Added

- New flag --rr-onprocess-pid to replay command (#3281, @jcpowermac)
- Added documentation for watching arbitrary address (#3268, @felixge)
- Allow extracting a DWARF entry field (#3258, @brancz)
- Add SetLoggerFactory to terminal/logflags package (#3257, @blaubaer)
- New config option for tab printing when printing source code (#3243, @thockin)
- Added documentation for debugging Go runtime with Delve (#3234, @aarzilli)

### Fixed

- Fix printing boolean values in Starlark scripts (#3314, @vitalif)
- Fix infinite recursion in escapeCheck (#3311, @aarzilli)
- Support multiple functions with same name (#3297, @aarzilli)
- Handle end_seq in dwarf/line correctly (#3277, @derekparker)
- Fix calls into SYS_PROCESS_VM_READV/WRITEV syscalls (#3273, @aarzilli)
- Fix exit status for trace subcommand (#3263, @derekparker)
- Fix stripping DYLD_INSERT_LIBRARIES on macOS (#3245, @aviramha)
- Fix handling of list colors via config (#3240, @derekparker)
- Fixes to FreeBSD backend (#3224, @aarzilli)

### Changed

- Add limit to maximum time.Time we try and format (#3294, @aarzilli)
- Add fuzzing tests to expression evaluator and variable loader (#3293, @aarzilli)
- Removed some support for Go 1.12 and earlier (#3271, @aarzilli)
- Moved util functions to dwarf package (#3252, @alexandear)
- Attempt to load DW_AT_specification if present (#3247, @brancz)
- Match go test behavior when dlv test gets list of go files (#3232, @aarzilli)
- Populate Value field in pointer Variable (#3229, @andreimatei)

## [1.20.1] 2022-12-12

### Fixed

- Fix executing programs on macOS with most versions of debugserver installed (#3211, @aarzilli)

## [1.20.0] 2022-12-07

### Added

- Support for Go 1.20 (#3129, #3196, #3180, @cuiweixie, @qmuntal, @aarzilli)
- Support for Windows/arm64 (gated by a build tag) (#3063, #3198, #3200, @qmuntal)
- Compatibility with coredumpctl (#3195, @Foxboron)

### Fixed

- Improve evaluation of type casts (#3146, #3149, #3186, @aarzilli)
- DAP: Added type to response of EvaluateRequest (#3172, @gfszr)
- Cgo stacktraces on linux/arm64 (#3192, @derekparker)
- Debugserver crashes on recent versions of macOS when $DYLD_INSERT_LIBRARIES is set (#3181, @aviramha)
- Stacktraces and stepping on Go 1.19.2 and later on macOS (#3204, @aarzilli)
- Attaching to processes used by a different user on Windows (#3162, @aarzilli)
- Nil pointer dereference when current address is not part of a function (#3157, @aarzilli)

### Changed

- Change behavior of exec command so that it looks for the executable in the current directory (#3167, @derekparker)
- DAP shows full value when evaluating log messages (#3141, @suzmue)
- Wait time is no longer reported for parked goroutines (its value was always incorrect) (#3139, @aarzilli)
- Miscellaneous improvements to documentation and error messages (#3119, #3117, #3154, #3161, #3169, #3188, @aarzilli, @derekparker, @cuishuang, @Frederick888,  @dlipovetsky)

## [1.9.1] 2022-08-23

### Added

- Add support for empty string in substitutePath (@RuijieC-dev)
- Support gnu_debuglink section (@aarzilli)
- Support exact matches in SubstitutePath (@eandre)
- Add ability to show disassembly instead of source code (@aazilli)
- Add -per-g-hitcount to breakpoint conditions (@yangxikun)

### Fixed

- Ensure breakpoint map exists (@aarzilli)
- Use standard library to compute CRC for gnu_debuglink section (@aarzilli)
- Fix command to download Go version in CI (@derekparker)
- Do not panic reading bad G struct (@aarzilli)
- Fix parsing DWARFv5 file table (@derekparker)
- Improve trace subcommand output (@derekparker)
- Fix documentation for examinemem (@aaarzilli)
- Fix step instruction on 1 byte instruction with software breakpoint (@qmuntal)
- Fix handling of function entry / return in ebpf tracing backend (@derekparker)
- Fix size of ebpf type for fn_addr (@derekparker)

### Changed

- Send large terminal output to pager (@aarzilli)
- Refactor windows backend framework (@qmuntal)
- Complete the dropping of CGO dependency for ebpf backend (@aarzilli)
- Limit disassembly range in DAP backend (@aarzilli)

## [1.9.0] 2022-07-06

### Added

- Support for Go 1.19 (#3038, #3031, #3009, @aarzilli)
- Autocomplete for local variables (#3004, @pippolo84)
- Support for function call injection on arm64 (#2996, @aarzilli)

### Fixed

- Ctrl-C handling on Windows (#3039, @aarzilli)
- Expressions accessing maps with string literals (#3036, @aarzilli)
- Occasional crash caused by race between manual stop and normal stop on macOS (#3021, @aarzilli)
- Pretty-print of register components (#3022, @aarzilli)
- Misc function call injection bugs (#3007, #3002, #3000, @aarzilli)

### Changed
- Improved FreeBSD port (#3019, #2972, #2981, #2982, @4a6f656c)
- Misc test fixes (#3011, #2995, #2979, @polinasok, @aarzilli)
- Misc documentation changes (#2998, #2991, @aarzilli, @polinasok)
- Better autogenerated function skip (#2975, @aarzilli)

## [1.8.3] 2022-04-25
### Added
- Pretty-print time.Time variables (@aarzilli)
- Better "could not open debug info" errors (@polinasok)
- DAP: Support --disable-aslr (@polinasok)
- DAP interface documentation improvements (@polinasok)
- CLI documentation improvements (@derekparker, @deathiop)

### Fixed
- DAP: offer disconnect/stop options for attach mode only (@polinasok)
- Fix godoc comments (@hitzhangjie)
- Allow low index == len in reslice expressions (@aarzilli)
- Fix leaky process when failing to debug stripped binaries in headless mode (@polinasok)
- Skip stepping into autogenerated functions for go1.18 (@aarzilli)

### Changed
- Drop support for building on Go < 1.10 (@aarzilli)

## [1.8.2] 2022-03-07
### Added
- Add '-clear' option for 'condition' command (@chainhelen)
- Support ctrl-Z for shell job control (@derekparker)

### Fixed
- Improve handling of hard coded breakpoints (@aarzilli)
- Better error messages for ambiguous function calls / type casts (@aarzilli)
- Fix crash when trying to open separate debug info (@aarzilli)
- Handle non-install dev tools on osx (@zchee)

### Changed
- Downgrade loadBuildID error to warning (@aarzilli)
- Require go-delve/liner in go.mod file instead of upstream version (@hyanhag)

## [1.8.1] 2022-02-07

### Added

- Downloading source code listings with debuginfod (@Foxboron)
- Added `transcript` command (@aarzilli)
- Enabled `dump` command on windows (@aarzilli)
- Env attribute in DAP launch requests (@hyangah)
- Better documentation for the DAP interface (@polinasok)

### Fixed

- Require argument for trace subcommand (@derekparker)
- Handling of inlined calls within inlined calls (@derekparker)
- Handling of DW_AT_inline attribute (@aarzilli)
- Set stop reason in StepInstruction (@suzmue)

### Changed

- The DAP interface will not create executables in the temp directory (@hyangah)
- When the `goroutines` command looks for the user frame it will exclude frames in internal and runtime/internal in addition to private runtime functions (@aarzilli)
- Breakpoints with hitcount conditions are automatically disabled when their condition can no longer be satisfied (@pippolo84)
- The commands `break` and `trace` will set a breakpoint on the current line if no argument is specified (@thockin)
- Miscellaneous documentation improvements (@chainhelen, @gareth-rees, @polinasok)

## [1.8.0] 2021-12-23

### Added

* Go 1.18 support
* Support for DWARF5 on Windows/MacOS (@aarzilli)
* Added more installation instructions to documentation (@polinasok)
* Allow for rewind to work after process exit with RR backend (@aarzilli)
* Added documentation on runtime.curg and runtime.frameoff in eval (@aarzilli)
* DAP: Expose sources command in evaluate request (@suzmue)
* DAP: Support Goroutine filters (@suzmue)

### Fixed

* Fix build version using buildinfo (@aarzilli)
* Fix crash when using deferred with no args (@kaddy-tom)
* DAP: Misc remote attach improvements (@polinasok)

### Changed

* Misc cleanup and refactoring (@aarzilli)
* Added option to disable invoking git during build (@herbygillot)
* Ignore 'pf' mappings during core dump creation (@aarzilli)

## [1.7.3] 2021-11-16

### Added

* Misc changes to prepare for Go 1.18 and generics (#2703, @2745, @aarzilli)
* Watchpoint support (disabled on Windows) (#2651, #2659, #2667, #2769, @aarzilli)
* Warn about listening to remote connections (#2721, @aarzilli)
* Support call injection with rr backend (#2740, @aarzilli)
* Support JSON-RPC and DAP on the same port from 'dlv debug/exec/test/attach' (#2755, @polinasok)
* DAP: Remote attach support (#2709, @polinasok)
* DAP: Multi-client support (#2731, #2737, #2781, @polinasok)
* DAP: Logpoints support (#2634, #2730, #2747, #2748, @suzmue)
* DAP: Dissasembly view support (#2713, #2716, #2728, #2742, @suzmue)
* DAP: Support dlvCwd and use a temp file as default debug binary (#2660, #2734, @hyangah, @polinasok)
* DAP: Auto-resume execution when setting breakpoints while running (#2726, @suzmue)
* DAP: Add --client-addr flag to run dap with a predefined client (#2568, @hyangah)
* DAP: Log parsed and applied launch configs (#2732, @polinasok)
* DAP: Add option to hide system goroutines (#2743, @suzmue)
* DAP: Add support for special 'config' expressions (#2750, @suzmue)

### Fixed

* Return correct exit status from Halt command (#2674, @suzmue)
* Merge register data before writing to register (#2699, @mknyszek)
* Do not assign temp breakpoint IDs to avoid conflicts with user breakpoints (#2650, @aarzilli)
* Miscellaneous fixes for Windows native backend (#2736, @aarzilli)
* Return error when assigning between function variables (#2692, @aarzilli)
* Obey logflags config for LoadConfig warnings (#2701, @aarzilli, @suzmue)
* Workaround for debugserver register set bug (#2770, @aarzilli)
* DAP: Fix nil dereference when byte array cannot be converted to string (#2733, @polinasok)
* DAP: Fix data race for noDebugProcess.ProcessState (#2735, @polinasok)

### Changed

* Refine handling of version-too-old errors (#2684, #2712, @polinasok, @yang-wei)
* eBPF tracing backend return value parsing (#2704, @derekparker)
* Replace libbpfgo with cilium/ebpf (##2771, @derekparker)
* DAP: Merge Arguments and Locals scopes (#2717, @suzmue)
* DAP: Refine launch/attach error visibility (#2671, @polinasok)
* DAP: Server refactoring to separate listener and client session layers (#2729, @polinasok)
* DAP: Improve shutdown logic and test coverage (#2749, @polinasok)

## [1.7.2] 2021-09-21

### Added

* Documentation: Add notes on porting Delve to other architectures (@aarzilli)
* Add internal checks to ensure we're synched with Go runtime internals (@aarzilli)
* eBPF backend can parse goroutine info (@derekparker)
* Add support for debuginfo-find (@derekparker)
* Add MAKE arguments for GOOS / GOARCH (@cmol)

### Fixed

* Correctly check for 1.17 and regabi (@aarzilli)
* Print config output strings quouted (@aarzilli, @krobelus)
* Update check for system goroutines (@suzmue)
* DAP: Halt before detach in Stop (@polinasok)
* DAP: Do not send halt request if debuggee is not running (@suzmue)

### Changed

* Include selected goroutine in threads request (@suzmue)
* Remove individual OS install instructions (@gabriel-vasile)
* DAP: Show decimal / hex values for uint (@suzmue)
* Avoid bright colors in default syntax highlighting (@krobelus)

## [1.7.1] 2021-08-18

### Added

- *EXPERIMENTAL* Added support for eBPF based trace backend (@derekparker)
- Added fuzzy completion for the CLI for commands and breakpoint locations (@derekparker)
- Added stack watchpoints (@aarzilli)
- Added verbose version output (@hyangah)
- DAP: Support for replay and core modes (@Iggomez)
- DAP: Added ability to page stack frames (@suzmue)
- DAP: Added len as metadata for maps (@suzmue)
- DAP: Add 'backend' launch/attach attribute (@polinasok)

### Fixed

- Fix handling of runtime throws (@derekparker)
- DAP: Handle unexpected debugger termination (@polinasok)

### Changed

- Added configuration for Target to not clear stepping breakpoints (@suzmue)
- Ignore existing breakpoints for continue-until (@derekparker)
- Improve help output for examinemem (@derekparker)
- Clarify next-while-nexting error (@suzmue)
- DWARF improvements for additional opcodes (@aarzilli)
- Treat SIGTERM as server disconnect signal (@polinasok)
- Update Cobra lib to v1.1.3 (@hyangah)
- Improvements to 'on' command (@aarzilli)
- Terminal will now prompt when breakpoint is hit during next/step/stepout (@aarzilli)
- DAP: Ensure server is always headless and target foregrounded (@polinasok)
- DAP: Set hit breakpoint IDs (@suzmue)

## [1.7.0] 2021-07-19

### Added

- Go 1.17 support (@aarzilli, @mknyszek)
- Add new API and terminal command for setting watchpoints (@aarzilli)
- Add filtering and grouping to goroutines command (@aarzilli)
- Added support for hit count condition on breakpoints (@suzmue, @aarzilli)
- DAP server: Handle SetVariable requests (@hyangah)
- DAP server: Add clipboard support (@hyangah)

### Fixed

- DAP server: Several shutdown / disconnect fixes (@suzmue, @polinasok)
- DAP server: Clean output executable name on Windows (@hyangah)
- DAP server: Variables response must not have null variables array (@polinasok)
- Fix runtimeTypeToDIE setup (necessary for Go 1.17) (@aarzilli)
- Reenable CGO stacktrace test on arm64 (@derekparker)
- Fix incorrect integer casts in freebsd C backend (@dwagin)
- Ensure correct exit status reported on commands following process death (@derekparker)
- Misc flakey test fixes / test refactoring (@polinasok)
- Fix for frame parameter being ignored in ConvertEvalScope when no goroutine is found (@suzmue)
- Ensure ContinueOnce returns StopExited if process exited, otherwise return StopUnknown (@polinasok)
- Fix panic in RPC2.ListDynamicLibraries (@derekparker)
- Fix typo in flag passed to check if debugserver supports unmask_signals (@staugust)

### Changed

- DAP server: Add sameuser security check (@hyangah)
- DAP server: Changes to context-dependent load limits for string type (@hyangah, @polinasok)
- DAP server: Add paging for arrays, slices and maps (@suzmue)
- DAP server: Deemphasize internal runtime stack frames (@suzmue)
- DAP server: Add throw reason to exception information upon panic (@suzmue)
- DAP server: Set breakpoint hit ID (@suzmue)
- DAP server: Add string value of byte/rune slice as child (@suzmue)
- Documentation: Add viminspector to list of editor plugins (@aarzilli)
- Support for ZMM registers in gdbserial backend (@aarzilli)
- Remove support for stack barriers (@derekparker)
- Improve support for DWARF5 (@derekparker)
- Improve documentation (@derekparker, @aarzilli)
- Print message and exit if Delve detects it is running under Rosetta on M1 macs (@aarzilli)
- Drop official Go 1.14 support (@derekparker)

## [1.6.1] 2021-05-18

### Added

- Dump command: generate core dumps from within Delve (@aarzilli)
- Toggle command: toggle breakpoints on or off (@alexsaezm)
- DAP server improvements (@polinasok, @hyangah, @suzmue)
- Delve now parses and uses the .eh_frame section when available (@aarzilli)
- Add linespec argument to 'continue' command (@icholy)
- Add optional format argument to 'print' and 'display' commands (@aarzilli)

### Fixed

- Fixed file reference handling with DWARF5 compilation units (@thanm)
- Fix embedded field searching (@aarzilli)
- Fix off by one error reading core files (@aarzilli)
- Correctly read G address on linux/arm64
- Avoid double removal of temp built binary (@polinasok)
- Fix temp binary deletion race in DAP server (@polinasok)
- Fix shutdown related bugs in RPC server (@aarzilli)
- Fix crashes induced by RequestManualStop (@aarzilli)
- Fix handling of DW_OP_piece (@aarzilli)
- Correctly truncate the result of binary operations on integers (@aarzilli)

### Changed

- Dropped Go 1.13 support (@aarzilli)
- Improved documentation (@ChrisHines, @aarzilli, @becheran, @hedenface, @andreimatei, @ehershey , @hyangah)
- Allow examinememory to use an expression (@felixge)
- Improve gdb server check on newer ARM based macs (@oxisto)
- CPU registers can now be used in expressions (@aarzilli)
- DAP: Add type information to variables (@suzmue)
- DAP: Support setting breakpoints while target is running (@polinasok)
- DAP: Implement function breakpoints (@suzmue)

## [1.6.0] 2021-01-28

### Added

- Support for debugging darwin/arm64 (i.e. macOS on Apple Silicon) (#2285, @oxisto)
- Support for debugging Go1.16 (#2214, @aarzilli)
- DAP: support for attaching to a local process (#2260, @polinasok)
- DAP: fill the `evaluateName` field when returning variables, enabling "Add to Watch" and "Copy as Expression" features of VSCode (#2292, @polinasok)
- Added WaitSince, WaitReason to `service/api.Goroutine` and to the `goroutines` command (#2264, #2283, #2270, @dlsniper, @nd, @aarzilli)
- Syntax highlighting for Go code (#2294, @aarzilli)
- Added flag `CallReturn` to `service/api.Thread` to distinguish return values filled by a `stepOut` command from the ones filled by a `call` command (#2230, @aarzilli)

### Fixed

- Fix occasional "Access is denied" error when debugging on Windows (#2281, @nd)
- Register formatting on ARM64 (#2289, @dujinze)
- Miscellaneous bug fixes (#2232, #2255, #2280, #2286, #2291, #2309, #2293, @aarzilli, @polinasok, @hitzhangjie)

### Changed

- The `goroutines` command can be interrupted by pressing Ctrl-C (#2278, @aarzilli)
- Using a TeamCity instance provided by JetBrains for Continuous Integration (#2298, #2307, #2311, #2315, #2326, @artspb, @nd, @aarzilli, @derekparker)
- Improvements to documentation and error messages (#2266, #2265, #2273, #2299, @andreimatei, @hitzhangjie, @zamai, @polinasok)

## [1.5.1] 2020-12-09

### Added
- DAP: Scope and variable requests, including call injection (#2111, #2158, #2160, #2184, #2185, @polinasok)
- DAP: Support removing breakpoints and breakpoint conditions (#2188, @polinasok)
- DAP: Support next, stepIn, stepOut requests (#2143, @polinasok)
- DAP: Miscellaneous improvements (#2167, #2186, #2238, #2233, #2248, @eliben, @suzmue, @polinasok)
- Command line flag `-r` to redirect standard file descriptors of the target process (#2146, #2222, @aarzilli)
- `-size` flag for `examinemem` command (##2147, @hitzhangjie)
- Command line flag to disable ASLR (#2202, @aarzilli)
- Support for DWARFv5 loclists (#2097, @aarzilli)

### Fixed
- Support for Go version >= 1.15.4 (#2235, @aarzilli)
- Fix displaying AVX registers (#2139, @aarzilli)
- Panic during `next`, `step` when there is no current goroutine (#2164, @chainhelen)
- Reading a deferred call's arguments on linux/arm64 (#2210, @aarzilli)
- Miscellaneous bug fixes (#2135, #2131, #2142, #2140, #2127, #2113, #2172, #2181, #2195, #2193, #2220, #2179, #2206, #2223, @aarzilli)

### Changed
- `dlv test` switches to the package directory like `go test` does (#2128, @aarzilli)
- Delve will no longer resolve symbolic links when searching for split debug_info if the executable path is not /proc/pid/exe (#2170, @aarzilli)
- Starlark scripts can now be interrupted using Ctrl-C even if they are not making any API calls (#2149, @aarzilli)
- An error message will be displayed more prominently if a connection is rejected due to the `--only-same-user` flag (which is enabled by default) (#2211, @aarzilli)
- Substitute path rules are applied to the argument of `break` and `trace` (#2213, @aarzilli)
- The output of `xcode-select --print-path` will be used to determine the location of `debugserver` instead of a hardcoded path (#2229, @aaronsky)
- Improvements to documentation and error messages (#2148, #2154, #2196, #2228, @aurkenb, @pohzipohzi, @chainhelen, @andreimatei)


## [1.5.0] 2020-07-29

### Added

- Go 1.15 support (#2011, @aarzilli)
- Added the `reload` command that restarts the debugging session after recompiling the program (#1971, @alexsaezm)
- Better support for printing pointers in the C part of a cgo program (#1997, @aarzilli)
- Some support for DWARFv5 (#2090, @aarzilli)

### Fixed

- Fixed trace subcommand when the `-p` option is used (#2069, @chainhelen)
- Nil pointer dereference when printing tracepoints (#2071, @aarzilli)
- Internal debugger error when printing the goroutine list of a corrupted or truncated core file (#2070, @aarzilli)
- Do not corrupt the list of source files whenever a plugin (or dynamically loaded library) is loaded (#2075, @aarzilli)
- Fixed current file/line reported after a segfault on macOS that was wrong under certain circumstances (#2081, @aarzilli)
- Internal debugger error when reading global variables of types using unsupported debug_info features (#2105, #2106, #2110, @aarzilli, @b00f)

### Changed

- Support for stack trace requests in DAP and other DAP improvements (#2056, #2093, #2099, #2103, @polinasok)
- Delve will step inside a private runtime function call when it is already inside the runtime package (#2061, @aarzilli)
- Updated cosiner/argv dependency to v0.1.0 (#2088, @gadelkareem)
- Improvements to documentation and error messages (#2068, #2084, #2091, @aarzilli, @bhcleek, @letientai299)

## [1.4.1] 2020-05-22

### Added

- Support for linux/386 added (@chainhelen)
- DAP server initial release (@polinasok, @eliben, @hyangah)
- New command `examinemem` (or `x`) allows users to examine raw memory (@chainhelen)
- New command `display` allows users to print value of an expression every time the program stops (@aarzilli)
- New flag `--tty` allows users to supply a TTY for the debugged program to use (@derekparker)
- Coredump support added for Arm64 (@ossdev07)
- Ability to print goroutine labels (@aarzilli)
- Allow printing registers for arbitrary stack frames (@aarzilli)
- Add `disassemble-flavor` to config to specify assembly syntax (@chainhelen)

### Fixed

- Allow function calls on non-struct types (@derekparker)
- Dwarf line parsing bug fix (@klemens-morgenstern)
- Improved error message when building Delve on unsupported systems (@aarzilli)
- Improved error message when trying to execute a binary in an invalid format for host system (@derekparker)
- Fix panic in Delve when using `call` command with some invalid input (@chainhelen)

### Changed

- Improved output from `dlv trace` and `trace` REPL commands (@derekparker)
- Conditional breakpoint performance improvements (@aarzilli)
- Thread register loading performance improvement on gdbserial backend (@derekparker)
- Reduce default log level to error (@aarzilli)
- Linux memory read/write optimization using process_vm_read/write (@cuviper)
- Terminal output of commands divided into categories (@aarzilli)
- Use less permissive file settings on history file (@derekparker)
- Autogenerated interface method calls wrappers now automatically stepped through (@aarzilli)

## [1.4.0] 2020-02-11

### Added

- Support for Linux/ARM64 (#1733, #1780 @hengwu0, @tykcd996)
- Support for Go 1.14 (@aarzilli)
- Added an API call that can be used by Delve front-ends to map between package names and build paths (#1784, @aarzilli)
- Added a field to goroutine objects returned by the API listing the goroutine's pprof labels (#1836, @nd)
- Better support for inlined functions (#1717, #1742, #1807 @aarzilli)

### Fixed

- Fixed target program crash after step-instruction (#1738, @aarzilli)
- Fixed miscellaneus bugs related to debugging Position Indepentent Executables and plugins (#1775, @aarzilli)
- Always remove breakpoints during detach (#1772, @hengwu0)
- Fixed Delve's exit status after the program has ended (#1781, @derekparker)
- Fixed nil pointer dereference in FunctionReturnLocations (#1789, @aarzilli)
- Improved performance of `goroutines -t` command (#1830, @aarzilli)
- Fixed occasional "Access Denied" error during attach on Windows (#1826, @alexbrainman)
- Fixed parsing of the `disassemble` command (#1837, @chainhelen)

### Changed

- Check that local connections originate from the same User ID as the one that started Delve's instance (#1764, @stapelberg)
- Mapping between package names and package paths is done using the DW_AT_go_package_name where available (#1757, @aarzilli)
- Improvements to documentation and error messages (#1806, #1822, #1827, #1843, #1848, #1850, #1853 @spacewander, @chainhelen, @stigok)
- Miscellaneous code refactorings (#1746, #1777, #1834 @derekparker, @aarzilli)

## [1.3.2] 2019-10-21

### Added

- New example for starlark documentation (@aarzilli)
- Allow calls to optimized functions (@aarzilli)
- Option to bypass smart stacktraces (@aarzilli)
- Ability to call method of embedded field (@chainhelen)
- Added `make unininstall` command (@chainhelen)
- User can re-record recorded targets (@aarzilli)

### Fixed

- Fix version reporting (current latest tagged as 1.3.1, reporting 1.3.0) (@derekparker)
- Fix nil pointer deref on proc.GetG (@aarzilli)
- Better handling of DW_TAG_inlined_subroutine without debug_line info (@aarzilli)
- Fix panic on invalid config values (@TerrySolar)
- Fix debug_line state machine behavior with multi-sequence units (@aarzilli)
- Fix starlark iteration on maps > 64 entries (@alxn)
- debug_frame parser fixed parsing augmentation (@chainhelen)
- Round TLS segment size to its alignment (@heschik)

### Changed

- Documentation bumped required Go version (@codekaup)
- Disassemble now works without a selected goroutine (@aarzilli)
- Correctly mark closure variables as shadowed (@aarzilli)
- Bump CI to use Go 1.13 (@aarzilli)

## [1.3.0] 2019-08-27

### Added

- Go 1.13 support (#1546, @aarzilli)
- Starlark scripting language in the command line client (#1466, #1605, @aarzilli, @derekparker)
- Initial support for FreeBSD (#1480, @rayrapetyan)
- Command line flag to continue process immediately after launch/attach (#1585, @briandealwis)
- Configuration option for the maximum recursion depth used when printing variables (#1626, @msaf1980)
- `next` command now takes a numerical option specifying how many times it should be repeated (#1629, @jeremyfaller)
- Command line options to redirect logs to a file or file descriptor (#1525, @aarzilli)
- Ability to read goroutine ancestors if they are enabled by passing `GODEBUG="tracebackancestors=N"` (requires Go >= 1.11) (#1514, #1570, @aarzilli)
- Breakpoint autocompletion for the command line client (#1612, @qingyunha)
- Added reverse step-instruction command for rr backend (#1596, @dpapastamos)
- Support debugging programs using plugins on Linux with Go 1.12 or later (#1413, #1414, @aarzilli) 
- Improved function call injection (#1503, #1504, #1548, #1591, #1602, @aarzilli)
- New variable flag to mark variables that have a fake or no-longer-valid address, because they are either stored in registers or in a stack frame that has been removed from the stack (#1619, @aarzilli)
- Support relative file paths when specifying breakpoint locations (#1478, @chainhelen)
- GetVersion API response now reports currently used backend (#1641, @aarzilli)
- `so` as alias for `stepout` (#1646, @stmuk)

### Fixed

- Fixed values of registers smaller than 64bit (#1583, @derekparker)
- Fixed reading maps with removed entries in Go 1.12 (#1532, @aarzilli)
- Fixed possible crash on Linux caused by function call injection (#1538, @aarzilli)
- Fixed errors reading DWARF sections (#1574, #1582, #1603, @aarzilli)
- Prompt to shutdown headless instance after the target process has exited (#1621, @briandealwis)
- Stacktraces when a SIGSEGV (or other signal) happens during a cgo call (#1647, @aarzilli)
- Error restarting program with next/step/stepout under some circumstances (#1657, @aarzilli)
- Other bug fixes (#1487, #1488, #1490, #1500, #1497, #1469, #1553, #1595, #1594, #1620, #1622, #1624, #1637, #1664, #1665, #1668, @derekparker, @aarzilli, @dpapastamos, @pjot726)

### Changed

- Delve will refuse to work with a version of Go that is either too old or too new (can be disabled with `--check-go-version=false`) (#1533, @aarzilli)
- When the value of a variable is determined to be a symbolic constant the numerical value of the symbolic constant will also be printed (#1530, @aarzilli)
- Catch fatal runtime errors (such as the deadlock detector triggering) automatically (#1502, @aarzilli)
- Replaced glide (which we weren't using anymore) with `go mod vendor` in make script (#1606, @derekparker)
- Removed support for reading interfaces in older versions (prior to 1.7) of Go (#1501, @aarzilli)
- Removed support for location expression "<fnname>:0" and associated API functionality (#1588, @aarzilli)
- Calling an unknown method through the JSON-RPC API will now return an error while previously it would simply be ignored (#1571, @aarzilli)
- Improved documentation and error messages (#1492, #1520, #1524, #1561, #1562, #1556, #1559, #1567, #1638, #1649, #1662, @derekparker, @Ladicle, @qaisjp, @justinclift, @tschundler, @two, @aarzilli, @dr2chase)

## [1.2.0] 2019-02-19

### Added

- Go 1.12 support
- Improved `trace` command to show return values and trace pre-built executables or tests (#1379, #1380, #1381, @derekparker)
- Windows Minidump support (#1386, #1387, #1402, @aarzilli)
- Better support for split DWARF symbol files (#1405, #1420, @derekparker, @slp)
- Function call support on macOS (#1324, @derekparker)
- `deferred` command to view the arguments of a deferred call (#1285, #1265, @aarzilli)
- Support for debugging Position Independent Executables (#1358, @aarzilli)
- Type conversions of byte and rune arrays into strings (#1372, @chainhelen)
- Configuration option (source-list-line-color) to change the color of line numbers in listings (#1364, @Russtopia)
- New expression `iface.(data)` to access the concrete value of interface variable `iface`, without having to write a full type assertion (#1340, @aarzilli)
- Support for specifying multiple source files as arguments for `debug`, `trace` and `test` (#1339, @chainhelen)

### Fixed

- Make `edit` command work with vim and neovim (#1451, @the4thamigo-uk)
- Support Linux kernels prior to 2.6.34 (i.e. without PTRACE_GETREGSET) (#1435, @aarzilli)
- Fixed `substitute-path` configuration option on Windows (#1418, @zavla)
- Better performance for ListGoroutines API call (#1440, #1408, @slp, @aarzilli)
- Better performance when loading the value of very large sparse maps (#1392, @aarzilli)
- Other bug fixes (#1377, #1384, #1429, #1434, #1445, @aarzilli)

### Changed

- Changes to where the configuration is stored, conforming to [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html) with fallbacks to the current directory when calls to `user.Current` fail (#1455 @GregorioMartinez, @acshekhara1)
- Project moved from github.com/derekparker/delve to github.com/go-delve/delve (@derekparker)
- Switched dependency management to go.mod (@aarzilli, @derekparker, @zavla)
- New build scripts and support building on macOS without the native backend (@aarzilli, @kevin-cantwell)
- Tolerate corrupted memory when reading the goroutine list (#1354, @aarzilli)
- Improved documentation and error messages (@sbromberger, @aarzilli, @derekparker, @chainhelen, @dishmaev, @altimac)

## [1.1.0] 2018-08-15

### Added

- Go 1.11 support (@aarzilli)
- Improvements to Delve logging (@aarzilli, @derekparker)
- Show global variables in disassembly (@aarzilli)
- Support for inlined calls (@aarzilli, @derekparker, @jaym)
- Support dwz compressed debug symbols (@slp)
- Support for debug info in a separate file (@slp)
- Let target process access the tty when running in headless mode on linux/native and darwin/gdbserial (@aarzilli)
- Commands `up` and `down` (@yasushi-saito)
- Flag to print stacktrace of all goroutines (@acshekhara1)
- Command `edit` (@benc153)
- Allow headless instances to keep running without a connected client (@aarzilli)
- Add `StartLoc` to `api.Goroutine` containing the starting location of each goroutine (@aarzilli)
- Preliminary support for function call injection with Go 1.11 (@aarzilli)
- Ability to read list of deferred calls of a goroutine (@aarzilli)

### Fixed

- Fixed crashes when configuration file can not be created (@derekparker, @yuval-k, @aarzilli)
- Fixed reported location of the go statement of goroutines (@derekparker)
- Allow evaluation of constants specified without the full package path (@aarzilli)
- Fixed some integer arithmetics bugs in proc (@functionary)
- Respect load configuration after reslicing a map (@aarzilli)
- Fixed race condition between Halt and process death in the linux native backend (@aarzilli)
- Support core files generated by gdb (@psanford)
- Fixed evaluation of breakpoint conditions containing a single boolean variable (@aarzilli)
- Miscellaneous bugs in the debug_line state machine (@aarzilli)

### Changed

- Removed redundant/obsolete methods of proc.Process Halt and Kill, general cleanup of native backends (@aarzilli)
- Improved documentation (@giuscri, @jsoref, @Carpetsmoker, @PatrickSchuster, @aarzilli, @derekparker, @ramya-rao-a, @dlsniper)
- Workaround in the gdbserial backend for broken version 902 of debugserver (@aarzilli)
- Changed operators || and && to short-circuit the evaluation of their arguments, like in Go (@aarzilli)
- Mark shadowed arguments as shadowed (@aarzilli)
- Allow syntax "package/path".varname to specify the full package path of a variable, in case disambiguating between multiple packages with the same name is necessary (@aarzilli)

## [1.0.0] 2018-02-19

### Added

- Print DWARF location expression with `whatis` (@aarzilli)
- Use `DW_AT_producer` to warn about optimized code (@aarzilli)
- Use constants to describe variable value (@aarzilli)
- Use `DW_AT_decl_line` to determine variable visibility (@aarzilli)
- `-offsets` flag for `stack` command (@aarzilli)
- Support CGO stacktraces (@aarzilli)
- Disable optimizations in C compiler (@aarzilli)
- `--output` flag to configure output binary (@Carpetsmoker)
- Support `DW_OP_piece`, `DW_OP_regX`, `DW_OP_fbreg` (@aarzilli)
- Support `DW_LNE_define_file` (@aarzilli)
- Support more type casts (@aarzilli)

### Fixed

- Disable file path case normalization on OSX (@aarzilli)
- Support Mozilla RR 5.1.0 (@aarzilli)
- Terminal no longer crashes when process exits during `next` (@aarzilli)
- Fix TestCoreFPRegisters on Go 1.9 (@aarzilli)
- Avoid scanning system stack if it's not executing CGO (@aarzilli)
- Locspec "+0" should always evaluate to the current PC (@aarzilli)
- Handle `DW_LNE_end_of_sequence` correctly (@aarzilli)
- Top level interface variables may have 0 address (@aarzilli)
- Handle `DW_TAG_subprogram` with a nochildren abbrev (@aarzilli)
- StepBreakpoint handling (@aarzilli)

### Changed

- Documentation improvements (@grahamking)
- Removed limitation of exit notifications (@dlsniper)
- Use `go env GOPATH` for install path
- Disable test caching (@aarzilli)
- Disable `-a` and use `all=` for Go 1.10 building (@aarzilli)
- Automatically deref interfaces on member access (@aarzilli)
- Replace all uses of `gosymtab/gopclntab` with `.debug_line` section (@aarzilli)

## [1.0.0-rc.2] 2017-10-16

### Added

- Automatically print panic reason for unrecovered panics (@aarzilli)
- Propagate frame offset to clients (@aarzilli)
- Added vim-delve plugin to documentation (@sebdah)
- Floating point register support in core files (@aarzilli)
- Go 1.9 support, including lexical block support (@aarzilli)
- Added whatis and config commands (@aarzilli)
- Add FrameOffset field to api.Stackframe (@aarzilli)

### Fixed

- Better interoperation with debugserver on macOS (@aarzilli / @dlsniper)
- Fix behavior of next, step and stepout with recursive functions (@aarzilli)
- Parsing of maps with zero sized values (@aarzilli)
- Typo in the documentation of `types` command (@custa)
- Data races in tests (@aarzilli)
- Fixed SetBreakpoint in native and gdbserial to return the breakpoint if it already exists (@dlsniper)
- Return breakpoint if it already exists (@dlsniper)
- Collect breakpoint information on exit from next/stepout/step (@aarzilli)
- Fixed install instructions (@jacobvanorder)
- Make headless server quit when the client disconnects (@aarzilli)
- Store the correct concrete value for interface variables (previously we would always have a pointer type, even when the concrete value was not a pointer) (@aarzilli)
- Fix interface and slice equality with nil (@aarzilli)
- Fix file:line location specs when relative paths are in .debug_line (@hyangah)
- Fix behavior of next/step/stepout in several edge-cases (invalid return addresses, no current goroutine, after process exists, inside unknown code, inside assembly files) (@aarzilli)
- Make sure the debugged executable we generated is deleted after exit (@alexbrainman)
- Make sure rr trace directories are deleted when we delete the executable and after tests (@aarzilli)
- Return errors for commands sent after the target process exited instead of panicking (@derekparker)
- Fixed typo in clear-checkpoint documentation (@iamzhout)

### Changed

- Switched from godeps to glide (@derekparker)
- Better performance of linux native backend (@aarzilli)
- Collect breakpoints information if necessary after a next, step or stepout command (@aarzilli)
- Autodereference escaped variables (@aarzilli)
- Use runtime.tlsg to determine G struct offset (@heschik)
- Use os.StartProcess to implement Launch on windows (@alexbrainman)
- Escaped variables are dereferenced instead of being reported as &v (@aarzilli)
- Report errors when we fail to load the executable on attach (@aarzilli)
- Distinguish between nil and empty slices and maps both in the API and on the command line interface (@aarzilli)
- Skip deferred functions on next and stepout (as long as they are not called through a panic) (@aarzilli)

## [1.0.0-rc.1] 2017-05-05

### Added

- Added support for core files (@heschik)
- Added support for lldb-server and debugserver as backend, using debugserver by default on macOS (@aarzilli)
- Added support for Mozilla RR as backend (@aarzilli)

### Fixed

- Detach should correctly kill child process we created (@aarzilli)
- Correctly return error when reading/writing memory of exited process (@aarzilli)
- Fix race condition in test (@hyangah)
- Fix version extraction to support proposals (@allada)
- Tolerate spaces better after command prefixes (@aarzilli)

### Changed

- Updated Mac OSX install instructions (@aarzilli)
- Refactor of core code in proc (@aarzilli)
- Improve list command (@aarzilli)

## [0.12.2] 2017-04-13

### Fixed

- Fix infinite recursion with pointer loop (@aarzilli)
- Windows: Handle delayed events (@aarzilli)
- Fix Println call to be Printf (@derekparker)
- Fix build on OSX (@koichi)
- Mark malformed maps as unreadable instead of panicking (@aarzilli)
- Fixed broken benchmarks (@derekparker)
- Improve reliability of certain tests (@aarzilli)

### Added

- Go 1.8 Compatability (@aarzilli)
- Add Go 1.8 to test matrix (@derekparker)
- Support NaN/Inf float values (@aarzilli)
- Handle absence of stack barriers in Go 1.9 (@drchase)
- Add gdlv to list of alternative UIs (@aarzilli)

### Changed

- Optimized 'trace' functionality (@aarzilli)
- Internal refactoring to support multiple backends, core dumps, and more (@aarzilli) [Still ongoing]
- Improve stacktraces (@aarzilli)
- Improved documentation for passing flags to debugged process (@njason)

## [0.12.1] 2017-01-11

### Fixed

- Fixed version output format.

## [0.12.0] 2017-01-11

### Added

- Added support for OSX 10.12.1 kernel update (@aarzilli)
- Added flag to set working directory (#650) (@rustyrobot)
- Added stepout command (@aarzilli)
- Implemented "attach" on Windows (@alexbrainman)
- Implemented next / step / step-instruction on parked goroutines (@aarzilli)
- Added support for App Engine (@dbenque)
- Go 1.7 support
- Added HomeBrew formula for installing on OSX.
- Delve now will break on unrecovered panics. (@aarzilli)
- Headless server can serve multiple clients.
- Conditional breakpoints have been implemented. (@aarzilli)
- Disassemble command has been implemented. (@aarzilli)
- Much improved documentation (still a ways to go).

### Changed

- Pretty printing: type of elements of interface slices are printed.
- Improvements in internal operation of "step" command.
- Allow quoting in build flags argument.
- "h" as alias for "help" command. (@stmuk)

### Fixed

- Improved prologue detection for large stack frames (#690) (@aarzilli)
- Fixed bugs involving stale executables during restart (#689) (@aarzilli)
- Various improvements to variable evaluation code (@aarzilli)
- Fix bug reading process comm name (@ggndnn)
- Add better detection for launching non executable files. (@aarzilli)
- Fix halt bug during tracing. (@aarzilli)
- Do not use escape codes on Windows when unsupported (@alexbrainman)
- Fixed path lookup logic on Windows. (@lukehoban)

## [0.11.0-alpha] 2016-01-26

### Added

- Windows support landed in master. Still work to be done, but 95% the way there. (@lukehoban)
- `step-instruction` command added, has same behavior of the old `step` command.
- (Backend) Implementation for conditional breakpoints, front end command coming soon. (@aarzilli)
- Implement expression evaluator, can now execute commands like `print i == 2`. (@aarzilli)

### Changed

- `step` command no longer steps single instruction but goes to next source line, stepping into functions.
- Refactor of `parseG` command for clarity and speed improvements.
- Optimize reading from target process memory with cache. (prefetch + parse) (@aarzilli)
- Shorten file paths in `trace` output.
- Added Git SHA to version output.
- Support function spec with partial package paths. (@aarzilli)
- Bunch of misc variable evaluation fixes (@aarzilli)

### Fixed

- Misc fixes in preparation for Go 1.6. (@aarzilli, @derekparker)
- Replace stdlib debug/dwarf with golang.org/x/debug/dwarf and fix Dwarf endian related parsing issues. (@aarzilli)
- Fix `goroutines` not working without an argument. (@aarzilli)
- Always clear temp breakpoints, even if normal breakpoint is hit. (@aarzilli)
- Infinite loading loop through maps. (@aarzilli)
- Fix OSX issues related to CGO memory corruption (array overrun in CGO). (@aarzilli)
- Fix OSX issue related to reporting multiple breakpoints hit at same time. (@aarzilli)
- Fix panic when using the `trace` subcommand.

## [0.10.0-alpha] 2015-10-04

### Added

- `set` command, allows user to set variable (currently only supports pointers / numeric values) (@aarzilli)
- All deps are vendored with Godeps and leveraging GO15VENDOREXPERIMENT
- `source` command and `--init` flag to run commands from a file (@aarzilli)
- `clearall` commands now take linespec (@kostya-sh)
- Support for multiple levels of struct nesting during variable eval (i.e. `print foo.bar.baz` now works) (@lukehoban)

### Changed

- Removed hardware assisted breakpoints (for now)
- Remove Go 1.4.2 on Travis builds

### Fixed

- Limit string sizes, be more tolerant of uninitialized memory (@aarzilli)
- `make` commands fixed for >= Go 1.5 on OSX
- Fixed bug where process would not be killed upon detach (@aarzilli)
- Fixed bug trying to detach/kill process that has already exited (@aarzilli)
- Support for "dumb" terminals (@dlsniper)
- Fix bug setting breakpoints at chanRecvAddrs (@aarzilli)

## [0.9.0-alpha] 2015-09-19

### Added

- Basic tab completion to terminal UI (@icholy)
- Added `-full` flag to stack command, prints local vars and function args (@aarzilli)

### Changed

- Output of threads and goroutines sorted by ID (@icholy)
- Performance improvement: cache parsed goroutines during halt (@icholy)
- Stack command no longer takes goroutine ID. Use scope prefix command instead (i.e. `goroutine <id> bt`)

### Fixed

- OSX: Fix hang when 'next'ing through highly parallel programs
- Absolute path confused as regexp in FindLocation (@aarzilli)
- Use sched.pc instead of gopc for goroutine location
- Exclude dead goroutines from `goroutines` command output (@icholy)

## [0.8.1-alpha] 2015-09-05

### Fixed
- OSX: Fix error setting breakpoint upon Delve startup.

## [0.8.0-alpha] 2015-09-05

### Added
- New command: 'frame'. Accepts a frame number and a command to execute in the context of that frame. (@aarzilli)
- New command: 'goroutine'. Accepts goroutine ID and optionally a command to execute within the context of that goroutine. (@aarzilli)
- New subcommand: 'exec'. Allows user to debug existing binary.
- Add config file and add config options for command aliases. (@tylerb)

### Changed
- Add Go 1.5 to travis list.
- Stop shortening file paths from API, shorten instead in terminal UI.
- Implemented several improvements for `next`ing through highly parallel programs.
- Visually align registers. (@paulsmith)

### Fixed
- Fixed output of 'goroutines' command.
- Stopped preserving temp breakpoints on restart.
- Added support for parsing multiple DWARF file tables. (@Omie)

## [0.7.0-alpha] 2015-08-14

### Added

- New command: 'list' (alias: 'ls'). Allows you to list the source code of either your current location, or a location that you describe via: file:line, line number (in current file), +/- offset or /regexp/. (@joeshaw)
- Added Travis-CI for continuous integration. Works for now, will eventually change.
- Ability to connect to headless server. When running Delve in headless mode (used previously only for editor integration), you now have the opportunity to connect to it from the command line with `dlv connect [addr]`. This will allow you to (insecurely) remotely debug an application. (@tylerb)
- Support for printing complex numeric types. (@ebfe)

### Changed

- Deprecate 'run' subcommand in favor of 'debug'. The 'run' subcommand now simply prints a warning, instructing the user to use the 'debug' command instead.
- All 'info' subcommands have been promoted to the top level. You can now simply run 'funcs', or 'sources' instead of 'info funcs', etc...
- Any command taking a location expression (i.e. break/trace/list) now support an updated linespec implementation. This allows you to describe the location you would like a breakpoint (etc..) set at in a more convenient way (@aarzilli).

### Fixed

- Improved support for CGO. (@aarzilli)
- Support for upcoming Go 1.5.
- Improve handling of soft signals on Darwin.
- EvalVariable evaluates package variables. (@aarzilli)
- Restart command now preserves breakpoints previously set.
- Track recurse level when eval'ing slices/arrays. (@aarzilli)
- Fix bad format string in cmd/dlv. (@moshee)
