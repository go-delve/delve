## dlv quirks

Quirks that a user may stumble on.

### Reading from stdin.

A Go program running under dlv cannot normally read from standard input. This will particularly affect interactive command line programs.

Programs that must read from stdin should use headless mode in two terminals:

```
// server
dlv debug main.go --headless --listen 127.0.0.1:3100

// client 
 dlv connect 127.0.1:3100
```
