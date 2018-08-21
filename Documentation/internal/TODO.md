# Internal documentation To-Do list

## General

* Develop a bit more the RPC client/server communication.

* Differentiate between commands handled by he server and the ones passed to the debugger.

* There's some portions of repeated code to handle the APv1/v2 that should be clarified. There's a `txtr` (?) readme inside `rpc1` that could be absorbed in this documentation, specially to note that APIv2 is the one used now and the one to be studied.

* How to connect to the API?

* How is the reflection resolved to index the (*RPCServer) methods.

* Some kind of SVG diagram of the high level architecture.

* Add a one-linesr description to https://github.com/derekparker/delve/blob/master/Documentation/cli/README.md for the user to at least know he's in the right place and not just start with "Commands".

* There is some "dynamic" and "static" themes/subject of the documentation. The general architecture is more or less a static picture, while the document describing how a CLI command triggers a Debugger function is more dynamic (regarding the code path). 

* Where does Delve stop execution at the beginning? It's not Main as someone new might expect, what are the main differences with Main? Is the Go runtime all set up at this point?


## Terminology

* Target vs Process

* attaching or launching? (*Debugger).New

* dlv command vs dlv debug command, is the second a subcommand?

* How to call the entire process (build, launch and attach) from `dlv debug` to having the command prompt?

* Delve's internals: what is describing? The inner workings of Delve beyond the user API?


## Questions

* optflags: what does the name represents? "optional flags"? "optimization flags"? The function only seems to generate flags for debugging.
