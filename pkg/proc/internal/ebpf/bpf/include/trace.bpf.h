#include "vmlinux.h"
#include "function_vals.bpf.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#define BPF_MAX_VAR_SIZ (1 << 24)  // 16MB — sufficient for compact events

// Ring buffer for HEADER and PARAM events to userspace.
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, BPF_MAX_VAR_SIZ);
} events SEC(".maps");

// Per-CPU scratch buffer for assembling one PARAM event before output.
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, u32);
    __type(value, char[SCRATCH_SIZE]);
} scratch SEC(".maps");

// Map which uses instruction address as key and function parameter info as the value.
struct {
    __uint(max_entries, 42);
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u64);
    __type(value, function_parameter_list_t);
} arg_map SEC(".maps");
