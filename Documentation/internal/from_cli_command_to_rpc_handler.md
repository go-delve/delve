# From CLI to the Debugger

This document describes the program flow in Delve's internals from a command typed by the user in the [CLI][CLI] in an interactive session to the  corresponding debugger functions that implement it, passing through the RPC client/server lower layer.

The correspondence between a CLI command and a debugger function is not directly one-to-one (although sometimes there is a clear correspondence), there is some processing in the RPC server layer which decides which debugger functions to call and what information to return.

* TODO: Link to terminal client and debug server in the gen_arch.md, refer to that document as an introduction for the user to have some reference.

* TODO: Debugger service or server? for now debugger service server/client, be consistent.

The terminal the user interacts with in the CLI during a debugging session is represented by the [`Term`][TERM] structure which contains the [list of commands][COMMANDS_LIST] available in its [`Commands`][COMMANDS] structure which is populated by [`DebugCommands`][DEBUGCOMMANDS] when the terminal is [created][TERM_NEW]. In `DebugCommands` one can find the function that implements each of the CLI commands (defined in its corresponding `cmdFn`).

The terminal has a [Client][CLIENT] structure to interact with the debugger service implemented with an RPC client. For example, the [`next`][NEXT_TERM] command from the terminal is implemented in part by calling the [`Next`][NEXT_CLIENT] debugger service client function. The debugger service client interface is implemented by the [`rpc2.RPCClient`][RPC2.RPCCLIENT] structure (RPCv2).

* TODO: Confirm that RPC2 is used (initialized (through `terminal.New`) in `execute` with an `rpc2.RPCClient`) and ask Derek the difference with RPC1.

* TODO: Expand on the call to RPC Server.

Normally those functions will have its counterpart in the [`rpc2.RPCServer`][RPC2.RPCSERVER], except for the ones that use the method `Command` (like the command `next` being analyzed) that is forwarded directly to the debugger's [`Command`](/service/debugger/debugger.go) function.

* TODO: RPC "Command" vs other commands with explicit names


## Appendix: RPC methods

The RPC methods supported by the server are stored in [`methodMaps`][METHODMAPS]. It is not self-evident how this structure gets populated (and later accessed), as the methods in charge of those tasks make heavy use of reflection. In its most simplified form it can be said that [`suitableMethods`][SUITABLEMETHODS] fills `methodMaps` with the defined methods that receive the [`RPCServer`][RPCSERVER] structure. There are actually many versions of `RPCServer` in `service/rpc1`, `service/rpc2` and `service/rpccommon/` for the APIv1, APIv2 and common API respectively.

Inside [`serveJSONCodec`][SERVEJSONCODEC] the JSON RPC will be decoded and the correct method (from the corresponding API) will be looked for in `methodMaps` to be called and its response will be returned.

* TODO: Which methods get loaded in methodMaps? Research suitableMethods.

* TODO: Continuation of the paragraph before, should be expanded later.


[CLI]: /Documentation/cli/
[COMMANDS]: /pkg/terminal/command.go#L63
[COMMANDS_LIST]: /Documentation/cli/#commands
[TERM]: /pkg/terminal/terminal.go#L26
[DEBUGCOMMANDS]: /pkg/terminal/command.go#L81
[TERM_NEW]: /pkg/terminal/terminal.go#L38
[CLIENT]: /service/client.go#L9
[NEXT_TERM]: /pkg/terminal/command.go#L820
[NEXT_CLIENT]: /service/client.go#L33
[RPC2.RPCCLIENT]: /service/rpc2/client.go#L14
[RPC2.RPCSERVER]: /service/rpc2/server.go#L13
[SUITABLEMETHODS]: /service/rpccommon/server.go#L183
[METHODMAPS]: /service/rpccommon/server.go#L42
[RPCSERVER]: /service/rpc2/server.go#L13
[SERVEJSONCODEC]: /service/rpccommon/server.go#L253
