# Known Bugs

- Delve does not currently support 32bit systems. This will usually manifest as a compiler error in `proc/disasm.go`. See [Issue #20](https://github.com/go-delve/delve/issues/20).
- When Delve is compiled with versions of go prior to 1.7.0 it is not possible to set a breakpoint on a function in a remote package using the `Receiver.MethodName` syntax. See [Issue #528](https://github.com/go-delve/delve/issues/528).
- When running Delve on binaries compiled with a version of go prior to 1.9.0 `locals` will print all local variables, including ones that are out of scope, the shadowed flag will be applied arbitrarily. If there are multiple variables defined with the same name in the current function `print` will not be able to select the correct one for the current line.
- `reverse step` will not reverse step into functions called by deferred calls.
