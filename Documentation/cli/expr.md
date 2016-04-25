# Expressions

Delve can evaluate a subset of go expression language, specifically the following features are supported:

- All (binary and unary) on basic types except <-, ++ and --
- Comparison operators on any type
- Type casts between numeric types
- Type casts of integer constants into any pointer type
- Struct member access (i.e. `somevar.memberfield`)
- Slicing and indexing operators on arrays, slices and strings
- Map access
- Pointer dereference
- Calls to builtin functions: `cap`, `len`, `complex`, `imag` and `real`
- Type assertion on interface variables (i.e. `somevar.(concretetype)`)

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

To use a field of a struct contained inside an interface variable use a type assertion:

```
(dlv) p iface1.(*main.astruct).B
2
```
