# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working
with code in this repository.

## Project Overview

Delve is a debugger for the Go programming language. This is a complex,
multi-layered system that requires understanding of debugging internals,
DWARF format, OS-specific process control, and Go runtime internals.

## Build and Test Commands

### Building

```bash
make build          # Build dlv binary
make install        # Install dlv to system
make uninstall      # Remove dlv from system
```

### Testing

```bash
make test                       # Run all tests with vetting
make vet                        # Run Go vet with architecture tags
go test -run TestName ./pkg/... # Run specific test by name
go test ./pkg/proc              # Run all pkg/proc tests
go test ./service/test          # Run all integration tests
```

### eBPF Backend Development

```bash
make build-ebpf-image   # Build Docker image for eBPF compilation
make build-ebpf-object  # Compile eBPF C code to object files
```

The eBPF backend uses Docker to compile C code
(`pkg/proc/internal/ebpf/bpf/trace.bpf.c`) in a controlled environment,
producing architecture-specific `.o` files.

#### eBPF Testing

eBPF tests (TestTraceEBPF*) require elevated capabilities (CAP_BPF,
CAP_PERFMON, CAP_SYS_RESOURCE) and must be run with sudo:

```bash
# Run specific eBPF test
sudo go test -v -run TestTraceEBPF3 -count 1 ./cmd/dlv

# Run all eBPF tests
sudo go test -v -run TestTraceEBPF -count 1 ./cmd/dlv

# Run eBPF tests in Docker (useful when host lacks capabilities or for CI)
docker run --privileged -v "$(pwd)":/delve -w /delve \
  -e GOFLAGS="-buildvcs=false" golang:1.24-bookworm \
  go test -v -run TestTraceEBPF -count 1 ./cmd/dlv

# Suppress debug output
sudo go test -v -run TestTraceEBPF3 -count 1 ./cmd/dlv 2>&1 | \
  grep -v "^DEBUG"
```

### Running Delve

```bash
dlv debug           # Compile and debug current package
dlv test            # Compile and debug tests
dlv attach <pid>    # Attach to running process
dlv exec <binary>   # Debug pre-compiled binary
dlv dap             # Start Debug Adapter Protocol server
```

## Test-Driven Development (MANDATORY)

**CRITICAL**: All code changes MUST follow test-driven development (TDD).
This is not optional.

### Red-Green-Refactor Cycle

1. **RED** - Write a failing test first (must fail for the right reason)
2. **GREEN** - Write minimum code to pass the test
3. **REFACTOR** - Clean up while keeping tests green
4. **VERIFY** - Run full test suite before committing

### Mandatory TDD Rules

1. **NEVER write implementation code without a failing test first**
2. **NEVER commit code without tests**
3. **NEVER skip tests or mark them as skipped to make CI pass**
4. **NEVER disable existing tests** - fix them or fix the code
5. Each test should verify ONE specific behavior
6. Test names should describe the scenario being tested

### Example Workflow

```bash
# RED - Write failing test
go test -run TestEvaluateComplexType ./pkg/proc
# Output: FAIL - undefined: evaluateComplexType (EXPECTED)

# GREEN - Implement minimum code
go test -run TestEvaluateComplexType ./pkg/proc
# Output: PASS

# REFACTOR - Clean up
go test -run TestEvaluateComplexType ./pkg/proc
# Output: PASS (must stay green)

# VERIFY - Full suite
make test
```

### Where to Write Tests

- **Unit tests**: Co-locate with code (e.g., `pkg/proc/variables_test.go`
  for `variables.go`)
- **Integration tests**: `service/test/` for end-to-end scenarios
- **Platform-specific tests**: Use build tags (e.g., `//go:build linux`)
- **Backend-specific tests**: Test each backend separately
- **Test fixtures**: Source code in `_fixtures/` (compiled during tests,
  not pre-compiled binaries)

### Test Quality Standards

- Test behavior, not implementation
- One assertion per test when possible (easier diagnosis)
- Clear naming: `Test<What>_<Scenario>_<ExpectedResult>`
- Use existing test utilities in `*_test.go` files
- Tests must be deterministic (no random values or race conditions)

### When TDD Seems Difficult

Difficulty writing tests first indicates: (1) function doing too much,
(2) tightly coupled dependencies, or (3) unclear requirements. **DO NOT
skip TDD** - the difficulty signals a design problem.

## Architecture Overview

Delve uses a **layered architecture** with clear separation of concerns:

```
CLI (cmd/dlv) → Cobra commands
    ↓
Service Layer (service/) → RPC2, DAP protocol implementations
    ↓
Debugger (service/debugger) → High-level debugging operations
    ↓
Process Abstraction (pkg/proc) → TargetGroup, Target, ProcessInternal
    ↓
Backend Implementations:
  - pkg/proc/native/        → Direct OS debugging (ptrace, Windows APIs)
  - pkg/proc/gdbserial/     → GDB remote protocol client
  - pkg/proc/core/          → Core dump analysis
  - pkg/proc/internal/ebpf/ → eBPF-based tracing (non-stop debugging)
```

### Process Abstraction (`pkg/proc`)

Core of Delve's architecture with **two-level interface design**:

- `Process` - Read-only public interface
- `ProcessInternal` - Internal interface with state-modifying operations
- `Target` - Wraps `ProcessInternal` with debugging state
- `TargetGroup` - Manages multiple processes (for exec following)
- `Thread` - OS thread abstraction
- `MemoryReadWriter` - Memory access abstraction

Backends are pluggable via `ProcessInternal` interface. When modifying
backends, changes usually apply to ALL implementations.

### Service Layer (`service/`)

- **`debugger/`** - `Debugger` struct wrapping `TargetGroup` with
  high-level operations (launch, attach, breakpoints, stepping, variable
  evaluation)
- **`rpc2/`** - JSON-RPC 2.0 server for remote clients
- **`dap/`** - Debug Adapter Protocol for IDE integration
- **`api/`** - Shared type definitions

### Binary Analysis

- **`pkg/dwarf/`** - Custom DWARF parser with Delve-specific features
- **`pkg/gobuild/`** - Go build system integration
- **`pkg/proc/BinaryInfo`** - Debug symbols, function entries, line tables

### Platform/Architecture Handling

Uses **filename-based conditional compilation**:

- OS-specific: `*_linux.go`, `*_darwin.go`, `*_windows.go`, `*_freebsd.go`
- Architecture-specific: `regs_amd64.go`, `regs_arm64.go`, etc.
- **DO NOT put platform-specific code in generic files**
- In most cases platform-specific code should only be added to the
  `pkg/proc/native` and `pkg/proc/gdbserial` backends.

Supported: amd64, arm64, 386, ppc64le, riscv64, loong64

## Development Guidelines

### Code Organization

1. **OS/arch separation**: Use Go's filename-based conditional
   compilation. Never put platform-specific code in generic files.

2. **Breakpoint types**: Two kinds exist:
   - `LogicalBreakpoint` - User-visible (set at file:line or function)
   - `Breakpoint` - Physical per target (multiple per logical breakpoint)

3. **Testing**: See "Test-Driven Development (MANDATORY)" section above.

### Working with DWARF

- Parsing in `pkg/dwarf/` with custom reader utilities
- Operations (expressions) in `pkg/dwarf/op/`
- Variable evaluation issues: check type reading
  (`pkg/proc/variables.go`), DWARF expression evaluation
  (`pkg/dwarf/op/`), and memory reading (`Process.Memory()`)

### Working with eBPF Backend

`pkg/proc/internal/ebpf/` is **fundamentally different**: uses **uprobes**
to trace function calls without stopping execution. C code compiles to
eBPF bytecode using Docker for reproducible builds. Manages goroutines for
event processing - be careful with shutdown.

Modifying eBPF code:

1. Edit `pkg/proc/internal/ebpf/bpf/trace.bpf.c`
2. Run `make build-ebpf-object`
3. Go code loads `.o` files using cilium/ebpf library

**Critical - Function Prologues**: Set breakpoints/uprobes at
`FirstPCAfterPrologue`, not `fn.Entry` (unless explicitly requested). Go's
stack-check prologue will re-trigger if set at `fn.Entry`.

### Commit Message Format

Use subsystem-based format from CONTRIBUTING.md:

```
<subsystem>: <what changed>

<why this change was made>

Fixes #<issue>
```

Subsystems: `proc`, `service`, `terminal`, `dwarf`, `native`, `dap`, `rpc2`,
`cmd/dlv`, etc.

**Subject line**: Max 70 characters, **Body**: Wrap at 80 characters

**Example**:

```
proc/internal/ebpf: fix goroutine leak and shutdown sequence

The eBPF event processing goroutines were not being properly cleaned up
on debugger shutdown, causing resource leaks. This adds proper context
cancellation and wait groups.

Fixes #1234
```

### Common Pitfalls

1. **Don't modify Process state directly** - Use `ProcessInternal` methods
   for locking and state consistency

2. **Breakpoint insertion is backend-specific** - `native` uses software
   breakpoints (INT3), `gdbserial` sends packets, `ebpf` uses uprobes

3. **Thread vs Goroutine** - Delve tracks both OS threads (`Thread`) and
   Go goroutines (`G` struct). Most operations work on goroutines, not
   threads.

4. **Memory safety** - Always handle errors when reading process memory.
   Process may die, memory may be unmapped, or addresses invalid.

## File Organization

```
delve/
├── cmd/dlv/                    # CLI entry point and commands
├── pkg/
│   ├── proc/                   # Core process abstraction
│   │   ├── native/            # OS-level debugging backends
│   │   ├── gdbserial/         # GDB remote protocol
│   │   ├── core/              # Core dump support
│   │   ├── internal/ebpf/     # eBPF tracing backend
│   │   └── *.go               # Process interfaces and common logic
│   ├── dwarf/                 # DWARF parsing and manipulation
│   ├── terminal/              # Interactive CLI
│   ├── config/                # Configuration (.delverc)
│   └── ...                    # Other utilities
├── service/
│   ├── debugger/              # High-level debugger API
│   ├── rpc2/                  # JSON-RPC 2.0 server
│   ├── dap/                   # Debug Adapter Protocol
│   └── api/                   # Shared type definitions
├── _fixtures/                 # Test source files (compiled during tests)
├── _scripts/                  # Build helper scripts
└── Documentation/             # User and internal documentation
```

## Dependencies

Key external dependencies (see `go.mod`):

- `github.com/cilium/ebpf` - eBPF program loading
- `github.com/google/go-dap` - Debug Adapter Protocol
- `github.com/spf13/cobra` - CLI framework
- `golang.org/x/arch` - CPU architecture utilities
- `golang.org/x/sys` - System calls and OS interfaces

## Resources

- [Internal Architecture Slides](https://speakerdeck.com/aarzilli/internal-architecture-of-delve)
- [Porting Guide](Documentation/internal/portnotes.md)
- [API Documentation](Documentation/api)
- [How to Write a Delve Client](Documentation/api/ClientHowto.md)
