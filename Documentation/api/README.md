# Server/Client API Documentation

Delve exposes two API interfaces, JSON-RPC and DAP, so that frontends other than the built-in [terminal client](../cli/README.md), such as [IDEs and editors](../EditorIntegration.md), can interact with Delve programmatically. The [JSON-RPC API](json-rpc/README.md) is used by the [terminal client](../cli/README.md), and will always stay up to date in lockstep regardless of new features. The [DAP API](dap/README.md) is a popular generic API already in use by many [tools](https://microsoft.github.io/debug-adapter-protocol/implementors/tools/).

## Usage

In order to run Delve in "API mode", simply invoke with one of the standard commands, providing the `--headless` flag, like so:

```
$ dlv debug --headless --api-version=2 --log --log-output=debugger,dap,rpc --listen=127.0.0.1:8181
```

This will start the debugger in a non-interactive mode, listening on the specified address, and will enable logging. The logging flags as well as the server address are optional, of course.

Optionally, you may also specify the `--accept-multiclient` flag if you would like to connect multiple JSON-RPC or DAP clients to the API.

You can connect to the headless debugger from Delve itself using the `connect` subcommand:

```
$ dlv connect 127.0.0.1:8181
```

This can be useful for remote debugging.

## API Interfaces

Delve has been architected in such a way as to allow multiple client/server implementations. All of the "business logic" as it were is abstracted away from the actual client/server implementations, allowing for easy implementation of new API interfaces.

### Current API Interfaces

- [JSON-RPC](json-rpc/README.md)
- [DAP](dap/README.md)
