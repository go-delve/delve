//go:build linux && amd64 && cgo && go1.16

package testhelper

// #include <stdbool.h>
// #include "../bpf/include/function_vals.bpf.h"
import "C"

type Function_parameter_t C.function_parameter_t
type Function_parameter_list_t C.function_parameter_list_t
