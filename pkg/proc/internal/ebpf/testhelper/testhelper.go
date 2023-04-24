//go:build linux && amd64 && cgo && go1.16
// +build linux,amd64,cgo,go1.16

package testhelper

// #include <stdbool.h>
// #include "../bpf/include/function_vals.bpf.h"
import "C"

// Function_parameter_t exports function_parameter_t from function_vals.bpf.h
type Function_parameter_t C.function_parameter_t

// Function_parameter_list_t exports function_parameter_list_t from function_vals.bpf.h
type Function_parameter_list_t C.function_parameter_list_t
