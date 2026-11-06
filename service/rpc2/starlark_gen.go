package rpc2

//go:generate go run github.com/go-delve/build-tools/cmd/gen-starlark-bindings@latest go ./starlark_mapping.go EvalStarlark,EvalStarlarkContinue,EvalStarlarkCancel
//go:generate go run github.com/go-delve/build-tools/cmd/gen-starlark-bindings@latest doc ../../Documentation/cli/starlark.md EvalStarlark,EvalStarlarkContinue,EvalStarlarkCancel
