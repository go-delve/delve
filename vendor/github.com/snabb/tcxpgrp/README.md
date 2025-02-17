tcxpgrp
=======

[![Go Reference](https://pkg.go.dev/badge/github.com/snabb/tcxpgrp.svg)](https://pkg.go.dev/github.com/snabb/tcxpgrp)
[![Go Report Card](https://goreportcard.com/badge/github.com/snabb/tcxpgrp)](https://goreportcard.com/report/github.com/snabb/tcxpgrp)

The Go package tcxpgrp implements POSIX.1 (IEEE Std 1003.1) tcgetpgrp
and tcsetpgrp functions.

There is also a function for determining if the calling process is a
foreground process.

This package is Linux/UNIX specific.


Documentation
-------------

https://pkg.go.dev/github.com/snabb/tcxpgrp


Example
-------

Determine if a process is foreground or background process:

```Go
	fg := tcxpgrp.IsForeground()
	fmt.Println("foreground:", fg)
	// Output: foreground: true
	//   or
	// Output: foreground: false
```


Repository
----------

https://github.com/snabb/tcxpgrp


License
-------

MIT
