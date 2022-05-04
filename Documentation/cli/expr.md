# Expressions

Delve can evaluate a subset of go expression language, specifically the following features are supported:

- All (binary and unary) on basic types except <-, ++ and --
- Comparison operators on any type
- Type casts between numeric types
- Type casts of integer constants into any pointer type and vice versa
- Type casts between string, []byte and []rune
- Struct member access (i.e. `somevar.memberfield`)
- Slicing and indexing operators on arrays, slices and strings
- Map access
- Pointer dereference
- Calls to builtin functions: `cap`, `len`, `complex`, `imag` and `real`
- Type assertion on interface variables (i.e. `somevar.(concretetype)`)

# Special Variables

Delve defines two special variables:

* `runtime.curg` evaluates to the 'g' struct for the current goroutine, in particular `runtime.curg.goid` is the goroutine id of the current goroutine.
* `runtime.frameoff` is the offset of the frame's base address from the bottom of the stack.

# Nesting limit

When delve evaluates a memory address it will automatically return the value of nested struct members, array and slice items and dereference pointers.
However to limit the size of the output evaluation will be limited to two levels deep. Beyond two levels only the address of the item will be returned, for example:

```
(dlv) print c1
main.cstruct {
	pb: *struct main.bstruct {
		a: (*main.astruct)(0xc82000a430),
	},
	sa: []*main.astruct len: 3, cap: 3, [
		*(*main.astruct)(0xc82000a440),
		*(*main.astruct)(0xc82000a450),
		*(*main.astruct)(0xc82000a460),
	],
}
```

To see the contents of the first item of the slice `c1.sa` there are two possibilities:

1. Execute `print c1.sa[0]`
2. Use the address directly, executing: `print *(*main.astruct)(0xc82000a440)`

# Elements limit

For arrays, slices, strings and maps delve will only return a maximum of 64 elements at a time:

```
(dlv) print ba
[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]
```

To see more values use the slice operator:

```
(dlv) print ba[64:]
[]int len: 136, cap: 136, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+72 more]
```

For this purpose delve allows use of the slice operator on maps, `m[64:]` will return the key/value pairs of map `m` that follow the first 64 key/value pairs (note that delve iterates over maps using a fixed ordering).

These limits can be configured with `max-string-len` and `max-array-values`. See [config](https://github.com/go-delve/delve/tree/master/Documentation/cli#config) for usage.

# Interfaces

Interfaces will be printed using the following syntax:
```
<interface name>(<concrete type>) <value>
```

For example:

```
(dlv) p iface1
(dlv) p iface1
interface {}(*struct main.astruct) *{A: 1, B: 2}
(dlv) p iface2
interface {}(*struct string) *"test"
(dlv) p err1
error(*struct main.astruct) *{A: 1, B: 2}
```

To use the contents of an interface variable use a type assertion:

```
(dlv) p iface1.(*main.astruct).B
2
```

Or just use the special `.(data)` type assertion:

```
(dlv) p iface1.(data).B
2
```

If the contents of the interface variable are a struct or a pointer to struct the fields can also be accessed directly:

```
(dlv) p iface1.B
2
```

# Specifying package paths

Packages with the same name can be disambiguated by using the full package path. For example, if the application imports two packages, `some/package` and `some/other/package`, both defining a variable `A`, the two variables can be accessed using this syntax:

```
(dlv) p "some/package".A
(dlv) p "some/other/package".A
```

# Pointers in Cgo

Char pointers are always treated as NUL terminated strings, both indexing and the slice operator can be applied to them. Other C pointers can also be used similarly to Go slices, with indexing and the slice operator. In both of these cases it is up to the user to respect array bounds.


# CPU Registers

The name of a CPU register, in all uppercase letters, will resolve to the value of that CPU register in the current frame. For example on AMD64 the expression `RAX` will evaluate to the value of the RAX register. 

Register names are shadowed by both local and global variables, so if a local variable called "RAX" exists, the `RAX` expression will evaluate to it instead of the CPU register.

Register names can optionally be prefixed by any number of underscore characters, so `RAX`, `_RAX`, `__RAX`, etc... can all be used to refer to the same RAX register and, in absence of shadowing from other variables, will all evaluate to the same value.

Registers of 64bits or less are returned as uint64 variables. Larger registers are returned as strings of hexadecimal digits.

Because many architectures have SIMD registers that can be used by the application in different ways the following syntax is also available:

* `REGNAME.intN` returns the register REGNAME as an array of intN elements.
* `REGNAME.uintN` returns the register REGNAME as an array of uintN elements.
* `REGNAME.floatN` returns the register REGNAME as an array fo floatN elements.

In all cases N must be a power of 2.

