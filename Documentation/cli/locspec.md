# Location Specifiers

Several delve commands take a program location as an argument, the syntax accepted by this commands is:

* `*<address>` Specifies the location of memory address *address*. *address* can be specified as a decimal, hexadecimal or octal number
* `<filename>:<line>` Specifies the line *line* in *filename*. *filename* can be the partial path to a file or even just the base name as long as the expression remains unambiguous.
* `<line>` Specifies the line *line* in the current file
* `+<offset>` Specifies the line *offset* lines after the current one
* `-<offset>` Specifies the line *offset* lines before the current one
* `<function>[:<line>]` Specifies the line *line* inside *function*. The full syntax for *function* is `<package>.(*<receiver type>).<function name>` however the only required element is the function name, everything else can be omitted as long as the expression remains unambiguous. 
* `/<regex>/` Specifies the location of all the functions matching *regex*
