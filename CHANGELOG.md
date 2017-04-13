# Changelog

All notable changes to this project will be documented in this file.
This project adheres to Semantic Versioning.

All changes mention the author, unless contributed by me (@derekparker).

## [0.12.2] 2017-04-13

### Fixed

- Fix infinite recursion with pointer loop (@aarzilli)
- Windows: Handle delayed events (@aarzilli)
- Fix Println call to be Printf (@derekparker)
- Fix build on OSX (@koichi)
- Mark malformed maps as unreadable instead of panicing (@aarzilli)
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
- Internal refactoring to support mutliple backends, core dumps, and more (@aarzilli) [Still ongoing]
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
- Allow quouting in build flags argument.
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
- Any command taking a location expression (i.e. break/trace/list) now support an updated linespec implementation. This allows you to describe the location you would like a breakpoint (etc..) set at in a more convienant way (@aarzilli).

### Fixed

- Improved support for CGO. (@aarzilli)
- Support for upcoming Go 1.5.
- Improve handling of soft signals on Darwin.
- EvalVariable evaluates package variables. (@aarzilli)
- Restart command now preserves breakpoints previously set.
- Track recurse level when eval'ing slices/arrays. (@aarzilli)
- Fix bad format string in cmd/dlv. (@moshee)
