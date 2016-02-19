# API Documentation

Delve exposes an API interface so that other programs, mostly IDEs and editors, can interact with Delve programmatically. The API is used by the terminal client, so will always stay up to date in lockstep regardless of new features.

## Usage

In order to run Delve in "API mode", simply invoke with one of the standard commands, providing the `--headless` flag, like so:

```
$ dlv debug --headless --log --listen=127.0.0.1:8181
```

This will start the debugger in a non-interactive mode, listening on the specified address, and will enable logging. The last two flags are optional, of course.

Optionally, you may also specify the `--accept-multiclient` flag if you would like to connect multiple clients to the API.

You can connect the headless debugger from Delve itself using the `connect` subcommand:

```
$ dlv connect 127.0.0.1:8181
```

This can be useful for remote debugging.

## API Interfaces

Delve has been architected in such a way as to allow multiple client/server implementations. All of the "business logic" as it were is abstracted away from the actual client/server implementations, allowing for easy implementation of new API interfaces.

### Current API Interfaces

- [JSON-RPC](json-rpc/README.md)
