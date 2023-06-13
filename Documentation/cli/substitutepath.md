## Path substitution configuration

Normally Delve finds the path to the source code that was used to produce an executable by looking at the debug symbols of the executable.
However, under [some circumstances](../faq.md#substpath), the paths that end up inside the executable will be different from the paths to the source code on the machine that is running the debugger. If that is the case Delve will need extra configuration to convert the paths stored inside the executable to paths in your local filesystem.

This configuration is done by specifying a list of path substitution rules.


### Where are path substitution rules specified

#### Delve command line client

The command line client reads the path substitution rules from Delve's YAML configuration file located at `$XDG_CONFIG_HOME/dlv/config.yml` or `.dlv/config.yml` inside the home directory on Windows.

The `substitute-path` entry should look like this:

```
substitute-path:
  - {from: "/compiler/machine/directory", to: "/debugger/machine/directory"}
  - {from: "", to: "/mapping/for/relative/paths"}
```

If you are starting a headless instance of Delve and connecting to it through `dlv connect` the configuration file that is used is the one that runs `dlv connect`.

The rules can also be modified while Delve is running by using the [config substitute-path command](./README.md#config):

```
(dlv) config substitute-path /from/path /to/path
```

Double quotes can be used to specify paths that contain spaces, or to specify empty paths:

```
(dlv) config substitute-path "/path containing spaces/" /path-without-spaces/
(dlv) config substitute-path /make/this/path/relative ""
```

#### DAP server

If you connect to Delve using the DAP protocol then the substitute path rules are specified using the substitutePath option in [launch.json](https://github.com/golang/vscode-go/blob/master/docs/debugging.md#launchjson-attributes).

```
	"substitutePath": [
		{ "from": "/from/path", "to": "/to/path" }
	]
```

The [debug console](https://github.com/golang/vscode-go/blob/master/docs/debugging.md#dlv-command-from-debug-console) can also be used to modify the path substitution list:

```
dlv config substitutePath /from/path /to/path
```

This command works similarly to the `config substitute-path` command described above.

### How are path substitution rules applied

Regardless of how they are specified the path substitution rules are an ordered list of `(from-path, to-path)` pairs. When Delve needs to convert a path P found inside the executable file into a path in the local filesystem it will scan through the list of rules looking for the first one where P starts with from-path and replace from-path with to-path.

Empty paths in both from-path and to-path are special, they represent relative paths: 

- `(from="" to="/home/user/project/src")` converts all relative paths in the executable to absolute paths in `/home/user/project/src`
- `(from="/build/dir" to="")` converts all paths in the executable that start with `/build/dir` into relative paths.

The path substitution code is SubstitutePath in pkg/locspec/locations.go.

