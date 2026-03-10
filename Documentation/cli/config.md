# Configuration

The configuration file `config.yml` is found in `$XDG_CONFIG_HOME/dlv` if `$XDG_CONFIG_HOME` is set, if it isn't set it will be in `$HOME/.config/dlv` on Linux and `$HOME/.dlv` in other operating systems.

The following options are available:

Option | Description
-------|------------
aliases | Map fo command aliases `command: [ "alias1", "alias2" ]`.
debug-info-directories | List of directories to use when searching for separate debug info files.
disassemble-flavor | Disassembler syntax. Can be 'intel', 'gun' or 'go'.
max-array-values | Maximum number of array values when printing variables.
max-string-len | Maximum string length used when printing variables.
max-variable-recurse | Maximum number of nested struct members when printing variables.
position | Controls how the current position in the program is displayed (source | disassembly | default).
prompt-color | Prompt color, as a terminal escape sequence.
show-location-expr | If true the 'whatis' command will print the DWARF location expression of its argument.
source-list-arrow-color | Source list arrow color, as a terminal escape sequence.
source-list-comment-color | Source list comment color, as a terminal escape sequence.
source-list-keyword-color | Source list keyword color, as a terminal escape sequence.
source-list-line-color | Source list line-number color, as a terminal escape sequence.
source-list-line-count | Number of lines to list above and below the cursor when printing source code.
source-list-number-color | Source list number color, as a terminal escape sequence.
source-list-string-color | Source list string color, as a terminal escape sequence.
source-list-tab-color | Source list tab color, as a terminal escape sequence.
stacktrace-basename-color | Color for the base name in paths in the stack trace, as a terminal escape sequence.
stacktrace-function-color | Color for function names in the stack trace, as a terminal escape sequence.
substitute-path | Path substitution rules, a list of `{ from: path, to: path }` pairs.
tab | Changes what is printed when a tab character is encountered in source code.
trace-show-timestamp | If true timestamps are shown in the trace output.

