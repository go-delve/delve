# General architecture

SVG or at least ascii with the main diagram of the client/server Delve architecture.

* Server and Debugger differentiate stacks.

* RPC Server and Client. The client is used in the terminal goroutine to connect to the server (and through it to the debugger).
