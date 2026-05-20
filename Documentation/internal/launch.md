# Launch and attach lifecycle

This note describes the path from a user command to the first stopped target.
It is meant as a map for contributors who need to understand startup behavior
before changing launch, attach, restart, or backend selection code.

## Entry points

Most command-line startup paths begin in `cmd/dlv/cmds/commands.go`:

* `debugCmd` builds a temporary binary for `dlv debug`, appends target
  arguments, and calls `execute` with `debugger.ExecutingGeneratedFile`.
* `testCmd` builds a temporary test binary for `dlv test`, chooses the package
  directory as the working directory when one was not supplied, and calls
  `execute` with `debugger.ExecutingGeneratedTest`.
* `attachCmd` parses the process id, keeps any executable path argument, and
  calls `execute` with that attach pid.
* `coreCmd` passes the executable path plus the core file path to `execute`.

The `execute` helper collects command-line flags into a `service.Config`. In
normal interactive mode it creates an in-memory listener/client pair with
`service.ListenerPipe`; in headless mode it opens the configured network
listener. Both modes then start a server through `rpccommon.NewServer`.

DAP launch and attach requests follow the same lower-level path. The DAP
server stores request fields in `s.config.Debugger` and then calls
`debugger.New` from `service/dap/server.go`.

## Server to debugger handoff

`service/rpccommon.ServerImpl.Run` is the JSON-RPC startup bridge. It normalizes
the API version, calls `debugger.New`, then exposes the resulting debugger
through the RPC2 server.

`service/debugger.New` decides what kind of target is being opened from
`debugger.Config`:

* `AttachPid` or `AttachWaitFor` calls `Debugger.Attach`.
* `CoreFile` opens an rr trace or core file.
* Otherwise `Debugger.Launch` starts a new process from `ProcessArgs`.

Only one of attach, core, or launch should be active for a debugger instance.
Launched targets can be restarted; attached processes and core files cannot.

## Backend selection

`Debugger.Launch` first verifies the executable format and then dispatches to a
backend:

* `native` calls `pkg/proc/native.Launch`.
* `lldb` calls `pkg/proc/gdbserial.LLDBLaunch`.
* `rr` records first, then replays through the gdbserial backend.
* `default` uses `lldb` on Darwin and `native` elsewhere.

`Debugger.Attach` mirrors this split with `native.Attach` and
`gdbserial.LLDBAttach`. This keeps backend choice centralized in
`service/debugger` while the OS-specific mechanics stay under `pkg/proc`.

## Native launch

The native backend has one `Launch` implementation per supported operating
system. On Linux, `pkg/proc/native/proc_linux.go`:

1. opens any requested stdin, stdout, and stderr redirects;
2. creates an `exec.Cmd` with the target argv and working directory;
3. sets `SysProcAttr.Ptrace` so the child starts under debugger control;
4. starts the child process and waits for the initial exec stop;
5. calls `nativeProcess.initialize`.

Other native backends have the same responsibility, but use the platform's
debugging API instead of Linux ptrace details.

## Target group setup

`nativeProcess.initialize` performs backend-independent target setup after the
OS-specific process handle exists:

1. `initializeBasic` loads initial process information and binary metadata.
2. A `proc.TargetGroup` is created with `proc.NewGroup`.
3. The first target is added through the returned `AddTargetFunc`.
4. The target group's initial stop reason is `proc.StopLaunched` for launched
   children and `proc.StopAttached` for attached processes.

`pkg/proc.TargetGroup` owns the selected target, logical breakpoints, follow-exec
settings, and event plumbing. When follow-exec is enabled, new exec'd processes
can be added to the same group; otherwise the group remains a single target.

## Cleanup ownership

Ownership differs by startup mode:

* For `dlv debug` and `dlv test`, the temporary binary is removed by the command
  handler after `execute` returns.
* `rpccommon.ServerImpl.Stop` kills launched targets on detach, but detaches
  without killing when Delve attached to an existing process.
* Restart reuses launch configuration and copies breakpoints into the new target
  group; it is intentionally disabled for attach and core-file sessions.

When changing startup behavior, check both the CLI and DAP paths. They converge
at `debugger.New`, but they prepare `debugger.Config` in different packages.
