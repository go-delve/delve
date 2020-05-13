// Package locspec implements code to parse a string into a specific
// location specification.
//
// Location spec examples:
//
// locStr ::= <filename>:<line> | <function>[:<line>] | /<regex>/ | (+|-)<offset> | <line> | *<address>
// * <filename> can be the full path of a file or just a suffix
// * <function> ::= <package>.<receiver type>.<name> | <package>.(*<receiver type>).<name> | <receiver type>.<name> | <package>.<name> | (*<receiver type>).<name> | <name>
// * <function> must be unambiguous
// * /<regex>/ will return a location for each function matched by regex
// * +<offset> returns a location for the line that is <offset> lines after the current line
// * -<offset> returns a location for the line that is <offset> lines before the current line
// * <line> returns a location for a line in the current file
// * *<address> returns the location corresponding to the specified address
package locspec
