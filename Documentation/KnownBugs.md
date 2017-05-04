# Known Bugs

- When a function defines two (or more) variables with the same name delve is unable to distinguish between them: `locals` will print both variables, `print` will randomly pick one. See [Issue #106](https://github.com/derekparker/delve/issues/106).
- Delve does not currently support 32bit systems. This will usually manifest as a compiler error in `proc/disasm.go`. See [Issue #20](https://github.com/derekparker/delve/issues/20).
- When Delve is compiled with versions of go prior to 1.7.0 it is not possible to set a breakpoint on a function in a remote package using the `Receiver.MethodName` syntax. See [Issue #528](https://github.com/derekparker/delve/issues/528).
- When running Delve on binaries compiled with a version of go prior to 1.9.0 `locals` will print all local variables, including ones that are out of scope. If there are multiple variables defined with the same name in the current function `print` will not be able to select the correct one for the current line.
