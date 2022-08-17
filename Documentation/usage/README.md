# Using Delve

You can invoke Delve in [multiple ways](dlv.md), depending on your usage needs. Delve makes every attempt to be user-friendly, ensuring the user has to do the least amount of work possible to begin debugging their program.

The [available commands](dlv.md) can be grouped into the following categories:

*  Specify target and start debugging with the default [terminal interface](../cli/README.md):
   * [dlv debug [package]](dlv_debug.md)
   * [dlv test [package]](dlv_test.md)
   * [dlv exec \<exe\>](dlv_exec.md)
   * [dlv attach \<pid\>](dlv_attach.md)
   * [dlv core \<exe\> \<core\>](dlv_core.md)
   * [dlv replay \<rr trace\> ](dlv_replay.md)
* Trace target program execution
   * [dlv trace [package] \<regexp\>](dlv_trace.md)
* Start a headless backend server only and connect with an external [frontend client](../EditorIntegration.md):
   * [dlv **--headless** \<command\> \<target\> \<args\> ](../api/ClientHowto.md#spawning-the-backend)
      * starts a server, enters a debug session for the specified target and waits to accept a client connection over JSON-RPC or DAP
      * `<command>` can be any of `debug`, `test`, `exec`, `attach`, `core` or `replay`
      * if `--headless` flag is not specified the default [terminal client](../cli/README.md) will be automatically started instead
      * compatible with [dlv connect](dlv_connect.md), [VS Code Go](https://github.com/golang/vscode-go/blob/master/docs/debugging.md#remote-debugging), [GoLand](https://www.jetbrains.com/help/go/attach-to-running-go-processes-with-debugger.html#attach-to-a-process-on-a-remote-machine)
   * [dlv dap](dlv_dap.md)
      * starts a DAP-only server and waits for a DAP client connection to specify the target and arguments
      * compatible with [VS Code Go](https://github.com/golang/vscode-go/blob/master/docs/debugging.md#remote-debugging)
      * NOT compatible with [dlv connect](dlv_connect.md), [GoLand](https://www.jetbrains.com/help/go/attach-to-running-go-processes-with-debugger.html#attach-to-a-process-on-a-remote-machine)
   * [dlv connect \<addr\>](dlv_connect.md)
      * starts a [terminal interface client](../cli/README.md) and connects it to a running headless server over JSON-RPC
* Help information
   * [dlv help [command]](dlv.md)
   * [dlv log](dlv_log.md)
   * [dlv backend](dlv_backend.md)
   * [dlv redirect](dlv_redirect.md)
   * [dlv version](dlv_version.md)

The above list may be incomplete. Refer to the auto-generated [complete usage document](dlv.md) to further explore all available commands.

## Environment variables

Delve also reads the following environment variables:

* `$DELVE_EDITOR` is used by the `edit` command (if it isn't set the `$EDITOR` variable is used instead)
* `$DELVE_PAGER` is used by commands that emit large output (if it isn't set the `$PAGER` variable is used instead, if neither is set `more` is used)
* `$TERM` is used to decide whether or not ANSI escape codes should be used for colorized output
* `$DELVE_DEBUGSERVER_PATH` is used to locate the debugserver executable on macOS
