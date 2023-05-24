# DAP Interface

Delve exposes a [DAP](https://microsoft.github.io/debug-adapter-protocol/overview) API interface.

This interface is served over a streaming TCP socket using `dlv` server in one of the two headless modes:
1. [`dlv dap`](../../usage/dlv_dap.md) - starts a single-use DAP-only server that waits for a client to specify launch/attach configuration for starting the debug session.
2. `dlv --headless <command> <debuggee>` - starts a general server, enters a debug session for the specified debuggee and waits for a [JSON-RPC](../json-rpc/README.md) or a [DAP](https://microsoft.github.io/debug-adapter-protocol/overview) remote-attach client to begin interactive debugging. Can be used in multi-client mode with the following options:
   *  `--accept-multiclient` - use to support connections from multiple clients
   *  `--continue` - use to resume debuggee execution as soon as server session starts

See [Launch and Attach Configurations](#launch-and-attach-configurations) for more usage details of these two options.

The primary user of this mode is [VS Code Go](https://github.com/golang/vscode-go). Please see its 
detailed [debugging documentation](https://github.com/golang/vscode-go/blob/master/docs/debugging.md) for additional information.

## Debug Adapter Protocol

[DAP](https://microsoft.github.io/debug-adapter-protocol/specification) is a general debugging protocol supported by many [tools](https://microsoft.github.io/debug-adapter-protocol/implementors/tools/) and [programming languages](https://microsoft.github.io/debug-adapter-protocol/implementors/adapters/). We tailored it to Go specifics, such as mapping [threads request](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_Threads) to communicate goroutines and [exceptionInfo request](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_ExceptionInfo) to support panics and fatal errors.

See [dap.Server.handleRequest](https://github.com/go-delve/delve/search?q=handleRequest) and capabilities set in [dap.Server.onInitializeRequest](https://github.com/go-delve/delve/search?q=onInitializeRequest) for an up-to-date list of supported requests and options.

## Launch and Attach Configurations

In addition to the general [DAP spec](https://microsoft.github.io/debug-adapter-protocol/specification), the server supports the following implementation-specific configuration options for starting the debug session:

<table border=1>
<tr><th>request<th>mode<th>required<th colspan=9>optional<th></tr>
<tr><td rowspan=5>launch<br><a href="https://pkg.go.dev/github.com/go-delve/delve/service/dap#LaunchConfig">godoc</a>
    <td>debug<td>program               <td>dlvCwd<td>env<td>backend<td>args<td>cwd<td>buildFlags<td>output<td>noDebug
    <td rowspan=7>
    substitutePath<br>
    stopOnEntry<br>
    stackTraceDepth<br>
    showGlobalVariables<br>
    showRegisters<br>
    hideSystemGoroutines<br>
    goroutineFilters
    </tr>
<tr>
    <td>test<td>program                <td>dlvCwd<td>env<td>backend<td>args<td>cwd<td>buildFlags<td>output<td>noDebug</tr>
<tr>
    <td>exec<td>program                <td>dlvCwd<td>env<td>backend<td>args<td>cwd<td>          <td>      <td>noDebug</tr>
<tr>
    <td>core<td>program<br>corefilePath<td>dlvCwd<td>env<td>       <td>    <td>   <td>          <td>      <td>       </tr>
<tr>
    <td>replay<td>traceDirPath         <td>dlvCwd<td>env<td>       <td>    <td>   <td>          <td>      <td>       </tr>
<tr><td rowspan=2>attach<br><a href="https://pkg.go.dev/github.com/go-delve/delve/service/dap#AttachConfig">godoc</a>
    <td>local<td>processId             <td>      <td>   <td>backend<td>   <td>    <td>          <td>      <td>        </tr>
<tr>
    <td>remote<td>                     <td>      <td>   <td>       <td>   <td>    <td>          <td>      <td>        </tr>
</table>


Not all of the configurations are supported by each of the two available DAP servers:

<table border=1>
<tr>
<th>request<th>"mode":<th>`dlv dap`<th>`dlv --headless` <th> Description <th> Typical Client Usage
</tr>
<tr>
<td>launch<td>"debug"<br>"test"<br>"exec"<br>"replay"<br>"core"<td>supported<td>NOT supported <td> Tells the `dlv dap` server to launch the specified target and start debugging it.
<td rowspan=2>The client would launch the `dlv dap` server for the user or allow them to specify `host:port` of an external (a.k.a. remote) server.
<tr>
<td>attach<td>"local"<td>supported<td>NOT supported<td>Tells the `dlv dap` server to attach to an existing process local to the server.
</tr>
<tr>
<td>attach<td>"remote"<td>NOT supported<td>supported<td>Tells the `dlv --headless` server that it is expected to already be debugging a target specified as part of its command-line invocation.
<td>The client would expect `host:port` specification of an external (a.k.a. remote) server that the user already started with target <a href="https://github.com/go-delve/delve/blob/master/Documentation/usage/README.md">command and args</a>.
</tr>
</table>

## Disconnect and Shutdown

### Single-Client Mode

When used with `dlv dap` or `dlv --headless --accept-multiclient=false` (default), the DAP server will shut itself down at the end of the debug session, when the client sends a [disconnect request](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_Disconnect). If the debuggee was launched, it will be taken down as well. If the debuggee was attached to, `terminateDebuggee` option will be respected.

When the program terminates, we send a [terminated event](https://microsoft.github.io/debug-adapter-protocol/specification#Events_Terminated), which is expected to trigger a [disconnect request](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_Disconnect) from the client for a session and a server shutdown. The [restart request](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_Restart) is not yet supported. 

The server also shuts down in case of a client connection error or SIGTERM signal, taking down a launched process, but letting an attached process continue. 

Pressing Ctrl-C on the terminal where a headless server is running sends SIGINT to the debuggee, foregrounded in headless mode to support debugging interactive programs.

### Multi-Client Mode

When used with `dlv --headless --accept-multiclient=true`, the DAP server will honor the multi-client mode when a client [disconnects](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_Disconnect) or client connection fails. The server will remain running and ready for a new client connection, and the debuggee will remain in whatever state it was at the time of disconnect - running or halted. Once [`suspendDebuggee`](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_Disconnect) option is supported by frontends like VS Code ([vscode/issues/134412](https://github.com/microsoft/vscode/issues/134412)), we will update the server to offer this as a way to specify debuggee state on disconnect.

The client may request full shutdown of the server and the debuggee with [`terminateDebuggee`](https://microsoft.github.io/debug-adapter-protocol/specification#Requests_Disconnect) option.

The server shuts down in response to a SIGTERM signal, taking down a launched process, but letting an attached process continue.

Pressing Ctrl-C on the terminal where a headless server is running sends SIGINT to the debuggee, foregrounded in headless mode to support debugging interactive programs.

## Debugger Output

The debugger always logs one of the following on start-up to stdout:
* `dlv dap`:
   * `DAP server listening at: <host>:<port>`
* `dlv --headless`: 
   * `API server listening at: <host>:<port>`

This can be used to confirm that server start-up succeeded.

The server uses [output events](https://microsoft.github.io/debug-adapter-protocol/specification#Events_Output) to communicate errors and select status messages to the client. For example:

```
Step interrupted by a breakpoint. Use 'Continue' to resume the original step command.
invalid command: Unable to step while the previous step is interrupted by a breakpoint.
Use 'Continue' to resume the original step command.
Detaching and terminating target process
```

More detailed logging can be enabled with `--log --log-output=dap` as part of the [`dlv` command](../../usage/dlv.md).
It will record the server-side DAP message traffic. For example, 
```
2022-01-04T00:27:57-08:00 debug layer=dap [<- from client]{"seq":1,"type":"request","command":"initialize","arguments":{"clientID":"vscode","clientName":"Visual Studio Code","adapterID":"go","locale":"en-us","linesStartAt1":true,"columnsStartAt1":true,"pathFormat":"path","supportsVariableType":true,"supportsVariablePaging":true,"supportsRunInTerminalRequest":true,"supportsMemoryReferences":true,"supportsProgressReporting":true,"supportsInvalidatedEvent":true}}
2022-01-04T00:27:57-08:00 debug layer=dap [-> to client]{"seq":0,"type":"response","request_seq":1,"success":true,"command":"initialize","body":{"supportsConfigurationDoneRequest":true,"supportsFunctionBreakpoints":true,"supportsConditionalBreakpoints":true,"supportsEvaluateForHovers":true,"supportsSetVariable":true,"supportsExceptionInfoRequest":true,"supportTerminateDebuggee":true,"supportsDelayedStackTraceLoading":true,"supportsLogPoints":true,"supportsDisassembleRequest":true,"supportsClipboardContext":true,"supportsSteppingGranularity":true,"supportsInstructionBreakpoints":true}}
2022-01-04T00:27:57-08:00 debug layer=dap [<- from client]{"seq":2,"type":"request","command":"launch","arguments":{"name":"Launch file","type":"go","request":"launch","mode":"debug","program":"./temp.go","hideSystemGoroutines":true,"__buildDir":"/Users/polina/go/src","__sessionId":"2ad0f0c1-a1fd-4fff-9fff-b8bc9a933fe5"}}
2022-01-04T00:27:57-08:00 debug layer=dap parsed launch config: {
	"mode": "debug",
	"program": "./temp.go",
	"backend": "default",
	"stackTraceDepth": 50,
	"hideSystemGoroutines": true
}
...
```
This logging is written to stderr and is not forwarded via 
[output events](https://microsoft.github.io/debug-adapter-protocol/specification#Events_Output).

## Debuggee Output

Debuggee's stdout and stderr are written to stdout and stderr respectfully and are not forwarded via 
[output events](https://microsoft.github.io/debug-adapter-protocol/specification#Events_Output).

## Versions

The initial DAP support was released in [v1.6.1](https://github.com/go-delve/delve/releases/tag/v1.6.1) with many additional improvements in subsequent versions. The [remote attach](https://github.com/go-delve/delve/issues/2328) support was added in [v1.7.3](https://github.com/go-delve/delve/releases/tag/v1.7.3).

The DAP API changes are backward-compatible as all new features are opt-in only. To update to a new [DAP version](https://microsoft.github.io/debug-adapter-protocol/changelog) and import a new DAP feature into delve, 
one must first update the [go-dap](https://github.com/google/go-dap) dependency.

<!--- TODO:
- most requests are handled synchronously and block
- hence many commands not supported when running, but setting breakpoints is
--->