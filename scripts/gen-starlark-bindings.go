package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// getSuitableMethods returns the list of methods of service/rpc2.RPCServer that are exported as API calls
func getSuitableMethods(pkg *types.Package, typename string) []*types.Func {
	r := []*types.Func{}
	mset := types.NewMethodSet(types.NewPointer(pkg.Scope().Lookup(typename).Type()))
	for i := 0; i < mset.Len(); i++ {
		fn := mset.At(i).Obj().(*types.Func)

		if !fn.Exported() {
			continue
		}

		if fn.Name() == "Command" {
			r = append(r, fn)
			continue
		}

		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			continue
		}

		// arguments must be (args, *reply)
		if sig.Params().Len() != 2 {
			continue
		}
		if ntyp, isname := sig.Params().At(0).Type().(*types.Named); !isname {
			continue
		} else if _, isstr := ntyp.Underlying().(*types.Struct); !isstr {
			continue
		}
		if _, isptr := sig.Params().At(1).Type().(*types.Pointer); !isptr {
			continue
		}

		// return values must be (error)
		if sig.Results().Len() != 1 {
			continue
		}
		if sig.Results().At(0).Type().String() != "error" {
			continue
		}

		r = append(r, fn)
	}
	return r
}

func fieldsOfStruct(typ types.Type) (fieldNames, fieldTypes []string) {
	styp := typ.(*types.Named).Underlying().(*types.Struct)
	for i := 0; i < styp.NumFields(); i++ {
		fieldNames = append(fieldNames, styp.Field(i).Name())
		fieldTypes = append(fieldTypes, styp.Field(i).Type().String())
	}
	return fieldNames, fieldTypes
}

func camelToDash(in string) string {
	out := []rune{}
	for i, ch := range in {
		if ch < 'A' || ch > 'Z' {
			out = append(out, ch)
			continue
		}

		if i != 0 {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(ch))
	}
	return string(out)
}

type binding struct {
	name string
	fn   *types.Func

	argType, retType string

	argNames []string
	argTypes []string
}

func processServerMethods(serverMethods []*types.Func) []binding {
	bindings := make([]binding, len(serverMethods))
	for i, fn := range serverMethods {
		sig, _ := fn.Type().(*types.Signature)
		argNames, argTypes := fieldsOfStruct(sig.Params().At(0).Type())

		name := camelToDash(fn.Name())

		switch name {
		case "set":
			// avoid collision with builtin that already exists in starlark
			name = "set_expr"
		case "command":
			name = "raw_command"
		default:
			// remove list_ prefix, it looks better
			const listPrefix = "list_"
			if strings.HasPrefix(name, listPrefix) {
				name = name[len(listPrefix):]
			}
		}

		retType := sig.Params().At(1).Type().String()
		if fn.Name() == "Command" {
			retType = "rpc2.CommandOut"
		}

		bindings[i] = binding{
			name:     name,
			fn:       fn,
			argType:  sig.Params().At(0).Type().String(),
			retType:  retType,
			argNames: argNames,
			argTypes: argTypes,
		}
	}
	return bindings
}

func removePackagePath(typePath string) string {
	lastSlash := strings.LastIndex(typePath, "/")
	if lastSlash < 0 {
		return typePath
	}
	return typePath[lastSlash+1:]
}

func genMapping(bindings []binding) []byte {
	buf := bytes.NewBuffer([]byte{})

	fmt.Fprintf(buf, "// DO NOT EDIT: auto-generated using scripts/gen-starlark-bindings.go\n\n")
	fmt.Fprintf(buf, "package starbind\n\n")
	fmt.Fprintf(buf, "import ( \"go.starlark.net/starlark\" \n \"github.com/go-delve/delve/service/api\" \n \"github.com/go-delve/delve/service/rpc2\" \n \"fmt\" )\n\n")
	fmt.Fprintf(buf, "func (env *Env) starlarkPredeclare() starlark.StringDict {\n")
	fmt.Fprintf(buf, "r := starlark.StringDict{}\n\n")

	for _, binding := range bindings {
		fmt.Fprintf(buf, "r[%q] = starlark.NewBuiltin(%q, func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {", binding.name, binding.name)
		fmt.Fprintf(buf, "if err := isCancelled(thread); err != nil { return starlark.None, decorateError(thread, err) }\n")
		fmt.Fprintf(buf, "var rpcArgs %s\n", removePackagePath(binding.argType))
		fmt.Fprintf(buf, "var rpcRet %s\n", removePackagePath(binding.retType))

		// unmarshal normal unnamed arguments
		for i := range binding.argNames {
			fmt.Fprintf(buf, "if len(args) > %d && args[%d] != starlark.None { err := unmarshalStarlarkValue(args[%d], &rpcArgs.%s, %q); if err != nil { return starlark.None, decorateError(thread, err) } }", i, i, i, binding.argNames[i], binding.argNames[i])

			switch binding.argTypes[i] {
			case "*github.com/go-delve/delve/service/api.LoadConfig":
				if binding.fn.Name() != "Stacktrace" {
					fmt.Fprintf(buf, "else { cfg := env.ctx.LoadConfig(); rpcArgs.%s = &cfg }", binding.argNames[i])
				}
			case "github.com/go-delve/delve/service/api.LoadConfig":
				fmt.Fprintf(buf, "else { rpcArgs.%s = env.ctx.LoadConfig() }", binding.argNames[i])
			case "*github.com/go-delve/delve/service/api.EvalScope":
				fmt.Fprintf(buf, "else { scope := env.ctx.Scope(); rpcArgs.%s = &scope }", binding.argNames[i])
			case "github.com/go-delve/delve/service/api.EvalScope":
				fmt.Fprintf(buf, "else { rpcArgs.%s = env.ctx.Scope() }", binding.argNames[i])
			}

			fmt.Fprintf(buf, "\n")

		}

		// unmarshal keyword arguments
		if len(binding.argNames) > 0 {
			fmt.Fprintf(buf, "for _, kv := range kwargs {\n")
			fmt.Fprintf(buf, "var err error\n")
			fmt.Fprintf(buf, "switch kv[0].(starlark.String) {\n")
			for i := range binding.argNames {
				fmt.Fprintf(buf, "case %q: ", binding.argNames[i])
				fmt.Fprintf(buf, "err = unmarshalStarlarkValue(kv[1], &rpcArgs.%s, %q)\n", binding.argNames[i], binding.argNames[i])
			}
			fmt.Fprintf(buf, "default: err = fmt.Errorf(\"unknown argument %%q\", kv[0])")
			fmt.Fprintf(buf, "}\n")
			fmt.Fprintf(buf, "if err != nil { return starlark.None, decorateError(thread, err) }\n")
			fmt.Fprintf(buf, "}\n")
		}

		fmt.Fprintf(buf, "err := env.ctx.Client().CallAPI(%q, &rpcArgs, &rpcRet)\n", binding.fn.Name())
		fmt.Fprintf(buf, "if err != nil { return starlark.None, err }\n")
		fmt.Fprintf(buf, "return env.interfaceToStarlarkValue(rpcRet), nil\n")

		fmt.Fprintf(buf, "})\n")
	}

	fmt.Fprintf(buf, "return r\n")
	fmt.Fprintf(buf, "}\n")

	return buf.Bytes()
}

func genDocs(bindings []binding) []byte {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "Function | API Call\n")
	fmt.Fprintf(&buf, "---------|---------\n")

	for _, binding := range bindings {
		argNames := strings.Join(binding.argNames, ", ")
		fmt.Fprintf(&buf, "%s(%s) | Equivalent to API call [%s](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.%s)\n", binding.name, argNames, binding.fn.Name(), binding.fn.Name())
	}

	fmt.Fprintf(&buf, "dlv_command(command) | Executes the specified command as if typed at the dlv_prompt\n")
	fmt.Fprintf(&buf, "read_file(path) | Reads the file as a string\n")
	fmt.Fprintf(&buf, "write_file(path, contents) | Writes string to a file\n")
	fmt.Fprintf(&buf, "cur_scope() | Returns the current evaluation scope\n")
	fmt.Fprintf(&buf, "default_load_config() | Returns the current default load configuration\n")

	return buf.Bytes()
}

const (
	startOfMappingTable = "<!-- BEGIN MAPPING TABLE -->"
	endOfMappingTable   = "<!-- END MAPPING TABLE -->"
)

func spliceDocs(docpath string, docs []byte, outpath string) {
	docbuf, err := ioutil.ReadFile(docpath)
	if err != nil {
		log.Fatalf("could not read doc file: %v", err)
	}

	v := strings.Split(string(docbuf), startOfMappingTable)
	if len(v) != 2 {
		log.Fatal("could not find start of mapping table")
	}
	header := v[0]
	v = strings.Split(v[1], endOfMappingTable)
	if len(v) != 2 {
		log.Fatal("could not find end of mapping table")
	}
	footer := v[1]

	outbuf := make([]byte, 0, len(header)+len(docs)+len(footer)+len(startOfMappingTable)+len(endOfMappingTable)+1)
	outbuf = append(outbuf, []byte(header)...)
	outbuf = append(outbuf, []byte(startOfMappingTable)...)
	outbuf = append(outbuf, '\n')
	outbuf = append(outbuf, docs...)
	outbuf = append(outbuf, []byte(endOfMappingTable)...)
	outbuf = append(outbuf, []byte(footer)...)

	if outpath != "-" {
		err = ioutil.WriteFile(outpath, outbuf, 0664)
		if err != nil {
			log.Fatalf("could not write documentation file: %v", err)
		}
	} else {
		os.Stdout.Write(outbuf)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "gen-starlark-bindings [doc|doc/dummy|go] <destination file>\n\n")
	fmt.Fprintf(os.Stderr, "Writes starlark documentation (doc) or mapping file (go) to <destination file>. Specify doc/dummy to generated documentation without overwriting the destination file.\n")
	os.Exit(1)
}

func main() {
	if len(os.Args) != 3 {
		usage()
	}
	kind := os.Args[1]
	path := os.Args[2]

	fset := &token.FileSet{}
	cfg := &packages.Config{
		Mode: packages.LoadSyntax,
		Fset: fset,
	}
	pkgs, err := packages.Load(cfg, "github.com/go-delve/delve/service/rpc2")
	if err != nil {
		log.Fatalf("could not load packages: %v", err)
	}

	var serverMethods []*types.Func
	packages.Visit(pkgs, func(pkg *packages.Package) bool {
		if pkg.PkgPath == "github.com/go-delve/delve/service/rpc2" {
			serverMethods = getSuitableMethods(pkg.Types, "RPCServer")
		}
		return true
	}, nil)

	bindings := processServerMethods(serverMethods)

	switch kind {
	case "go":
		mapping := genMapping(bindings)

		outfh := os.Stdout
		if path != "-" {
			outfh, err = os.Create(path)
			if err != nil {
				log.Fatalf("could not create output file: %v", err)
			}
			defer outfh.Close()
		}

		src, err := format.Source(mapping)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s", string(mapping))
			log.Fatal(err)
		}
		outfh.Write(src)

	case "doc":
		docs := genDocs(bindings)
		spliceDocs(path, docs, path)

	case "doc/dummy":
		docs := genDocs(bindings)
		spliceDocs(path, docs, "-")

	default:
		usage()
	}
}
