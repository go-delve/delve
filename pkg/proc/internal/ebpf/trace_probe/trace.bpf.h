#include "vmlinux.h"
#include "function_vals.bpf.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#define BPF_MAX_VAR_SIZ	(1 << 29)

// Ring buffer to handle communication of variable values back to userspace.
struct {
   __uint(type, BPF_MAP_TYPE_RINGBUF);
   __uint(max_entries, BPF_MAX_VAR_SIZ);
} events SEC(".maps");

struct {
   __uint(type, BPF_MAP_TYPE_RINGBUF);
   __uint(max_entries, BPF_MAX_VAR_SIZ);
} heap SEC(".maps");

struct {
    __uint(max_entries, 42);
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u64);
    __type(value, function_parameter_list_t);
} arg_map SEC(".maps");
