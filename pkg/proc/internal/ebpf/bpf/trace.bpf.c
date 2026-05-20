#include "include/trace.bpf.h"

#define POINTER_KIND 22
#define SLICE_KIND 23
#define STRING_KIND 24

__always_inline
int parse_string_param(struct pt_regs *ctx, function_parameter_t *param) {
    u64 str_len;
    size_t str_addr;

    __builtin_memcpy(&str_addr, param->val, sizeof(str_addr));
    __builtin_memcpy(&str_len, param->val + sizeof(str_addr), sizeof(str_len));
    param->daddr = str_addr;

    if (str_addr != 0) {
        if (str_len > MAX_VAL_SIZE) {
            str_len = MAX_VAL_SIZE;
        }
        int ret = bpf_probe_read_user(&param->deref_val, str_len, (void *)(str_addr));
        if (ret < 0) {
            return 1;
        }
    }
    return 0;
}

__always_inline
int parse_pointer_param(struct pt_regs *ctx, function_parameter_t *param) {
    size_t ptr_addr;
    __builtin_memcpy(&ptr_addr, param->val, sizeof(ptr_addr));
    param->daddr = ptr_addr;
    if (ptr_addr != 0) {
        int ret = bpf_probe_read_user(&param->deref_val, MAX_VAL_SIZE, (void *)(ptr_addr));
        if (ret < 0) {
            return 1;
        }
    }
    return 0;
}

__always_inline
int parse_slice_param(struct pt_regs *ctx, function_parameter_t *param) {
    size_t data_ptr;
    __builtin_memcpy(&data_ptr, param->val, sizeof(data_ptr));
    param->daddr = data_ptr;
    if (data_ptr != 0) {
        int ret = bpf_probe_read_user(&param->deref_val, MAX_VAL_SIZE, (void *)(data_ptr));
        if (ret < 0) {
            return 1;
        }
    }
    return 0;
}

// get_value_from_register copies a register value from the snapshotted regs[]
// array into dest.
__always_inline
void get_value_from_register(u64 *regs, void *dest, int reg_num) {
    switch (reg_num) {
    case 0:  __builtin_memcpy(dest, &regs[0],  8); break; // RAX
    case 1:  __builtin_memcpy(dest, &regs[1],  8); break; // RDX
    case 2:  __builtin_memcpy(dest, &regs[2],  8); break; // RCX
    case 3:  __builtin_memcpy(dest, &regs[3],  8); break; // RBX
    case 4:  __builtin_memcpy(dest, &regs[4],  8); break; // RSI
    case 5:  __builtin_memcpy(dest, &regs[5],  8); break; // RDI
    case 6:  __builtin_memcpy(dest, &regs[6],  8); break; // RBP
    case 7:  __builtin_memcpy(dest, &regs[7],  8); break; // RSP
    case 8:  __builtin_memcpy(dest, &regs[8],  8); break; // R8
    case 9:  __builtin_memcpy(dest, &regs[9],  8); break; // R9
    case 10: __builtin_memcpy(dest, &regs[10], 8); break; // R10
    case 11: __builtin_memcpy(dest, &regs[11], 8); break; // R11
    case 12: __builtin_memcpy(dest, &regs[12], 8); break; // R12
    case 13: __builtin_memcpy(dest, &regs[13], 8); break; // R13
    case 14: __builtin_memcpy(dest, &regs[14], 8); break; // R14
    case 15: __builtin_memcpy(dest, &regs[15], 8); break; // R15
    }
}

// read_register_value reads a parameter's value from snapshotted registers
// into param->val using the fallthrough switch pattern.
__always_inline
int read_register_value(u64 *regs, function_parameter_t *param) {
    switch (param->n_pieces) {
    case 6:
        get_value_from_register(regs, param->val+40, param->reg_nums[5]);
        __attribute__((fallthrough));
    case 5:
        get_value_from_register(regs, param->val+32, param->reg_nums[4]);
        __attribute__((fallthrough));
    case 4:
        get_value_from_register(regs, param->val+24, param->reg_nums[3]);
        __attribute__((fallthrough));
    case 3:
        get_value_from_register(regs, param->val+16, param->reg_nums[2]);
        __attribute__((fallthrough));
    case 2:
        get_value_from_register(regs, param->val+8, param->reg_nums[1]);
        __attribute__((fallthrough));
    case 1:
        get_value_from_register(regs, param->val, param->reg_nums[0]);
    }
    return 0;
}

// read_stack_value reads a parameter's value from the user stack.
__always_inline
int read_stack_value(struct pt_regs *ctx, function_parameter_t *param) {
    size_t addr = ctx->sp + param->offset;
    unsigned int sz = param->size;
    if (sz > MAX_VAL_SIZE) {
        sz = MAX_VAL_SIZE;
    }
    long ret = bpf_probe_read_user(&param->val, sz, (void *)(addr));
    if (ret < 0) {
        return 1;
    }
    return 0;
}

// parse_param reads the raw value (from registers or stack), then dispatches
// to type-specific parsers for strings, pointers, and slices.
__always_inline
int parse_param(u64 *regs, struct pt_regs *ctx, function_parameter_t *param) {
    if (param->size > MAX_VAL_SIZE) {
        return 0;
    }

    int ret = 0;
    if (param->in_reg) {
        ret = read_register_value(regs, param);
    } else {
        ret = read_stack_value(ctx, param);
    }
    if (ret != 0) {
        return ret;
    }

    switch (param->kind) {
        case POINTER_KIND:
            return parse_pointer_param(ctx, param);
        case SLICE_KIND:
            return parse_slice_param(ctx, param);
        case STRING_KIND:
            return parse_string_param(ctx, param);
    }

    return 0;
}

// get_goroutine_id reads the goroutine ID from thread-local storage.
__always_inline
long long get_goroutine_id(function_parameter_list_t *args) {
    struct task_struct *task;
    size_t g_addr;
    __u64 goid;

    task = (struct task_struct *)bpf_get_current_task();
    bpf_probe_read_user(&g_addr, sizeof(void *),
        (void *)(BPF_CORE_READ(task, thread.fsbase) + args->g_addr_offset));
    bpf_probe_read_user(&goid, sizeof(void *),
        (void *)(g_addr + args->goid_offset));
    return (long long)goid;
}

// WRITE_AND_OUTPUT_PARAM: parse a single param, build a param_event_data_t,
// and submit it to the ring buffer.
#define WRITE_AND_OUTPUT_PARAM(regs, ctx, param, goid, fnaddr, idx, isret) \
    do {                                                                   \
        parse_param(regs, ctx, param);                                     \
        param_event_data_t *pe = bpf_ringbuf_reserve(&events,             \
            sizeof(param_event_data_t), 0);                                \
        if (!pe) break;                                                    \
        pe->hdr.type = EVENT_TYPE_PARAM;                                   \
        pe->hdr.goroutine_id = (goid);                                     \
        pe->hdr.fn_addr = (fnaddr);                                        \
        pe->hdr.param_idx = (idx);                                         \
        pe->hdr.is_ret = (isret);                                          \
        pe->hdr.kind = (param)->kind;                                      \
        pe->hdr.val_size = (param)->size;                                   \
        __builtin_memcpy(pe->val, (param)->val, MAX_VAL_SIZE);              \
        __builtin_memcpy(pe->deref_val, (param)->deref_val, MAX_VAL_SIZE); \
        bpf_ringbuf_submit(pe, BPF_RB_FORCE_WAKEUP);                      \
    } while (0)

SEC("uprobe/dlv_trace")
int uprobe__dlv_trace(struct pt_regs *ctx) {
    function_parameter_list_t *args;
    uint64_t key = ctx->ip;

    args = bpf_map_lookup_elem(&arg_map, &key);
    if (!args) {
        return 1;
    }

    // Snapshot registers into a stack array so we can pass them around
    // without re-reading ctx fields (helps BPF verifier).
    u64 regs[16];
    regs[0]  = ctx->ax;
    regs[1]  = ctx->dx;
    regs[2]  = ctx->cx;
    regs[3]  = ctx->bx;
    regs[4]  = ctx->si;
    regs[5]  = ctx->di;
    regs[6]  = ctx->bp;
    regs[7]  = ctx->sp;
    regs[8]  = ctx->r8;
    regs[9]  = ctx->r9;
    regs[10] = ctx->r10;
    regs[11] = ctx->r11;
    regs[12] = ctx->r12;
    regs[13] = ctx->r13;
    regs[14] = ctx->r14;
    regs[15] = ctx->r15;

    // Get goroutine ID.
    long long goid = get_goroutine_id(args);

    // Determine which parameter set to use.
    unsigned int n_params;
    function_parameter_t *params;
    if (!args->is_ret) {
        n_params = args->n_parameters;
        params = args->params;
    } else {
        n_params = args->n_ret_parameters;
        params = args->ret_params;
    }
    if (n_params > MAX_PARAMS_PER_EVENT) {
        n_params = MAX_PARAMS_PER_EVENT;
    }

    // Emit the event header.
    event_header_t *hdr = bpf_ringbuf_reserve(&events, sizeof(event_header_t), 0);
    if (!hdr) {
        return 1;
    }
    hdr->type = EVENT_TYPE_HEADER;
    hdr->goroutine_id = goid;
    hdr->fn_addr = args->fn_addr;
    hdr->is_ret = args->is_ret;
    hdr->n_params = n_params;
    bpf_ringbuf_submit(hdr, BPF_RB_FORCE_WAKEUP);

    // Emit one param event per parameter (unrolled; BPF forbids loops).
    // Unlike read_register_value above, a fallthrough switch doesn't work
    // here because each guard is independent (param 1 doesn't depend on
    // param 2), and fallthrough would reverse the emission order.
    if (n_params >= 1) {
        WRITE_AND_OUTPUT_PARAM(regs, ctx, &params[0], goid, args->fn_addr, 0, args->is_ret);
    }
    if (n_params >= 2) {
        WRITE_AND_OUTPUT_PARAM(regs, ctx, &params[1], goid, args->fn_addr, 1, args->is_ret);
    }
    if (n_params >= 3) {
        WRITE_AND_OUTPUT_PARAM(regs, ctx, &params[2], goid, args->fn_addr, 2, args->is_ret);
    }
    if (n_params >= 4) {
        WRITE_AND_OUTPUT_PARAM(regs, ctx, &params[3], goid, args->fn_addr, 3, args->is_ret);
    }
    if (n_params >= 5) {
        WRITE_AND_OUTPUT_PARAM(regs, ctx, &params[4], goid, args->fn_addr, 4, args->is_ret);
    }
    if (n_params >= 6) {
        WRITE_AND_OUTPUT_PARAM(regs, ctx, &params[5], goid, args->fn_addr, 5, args->is_ret);
    }

    return 0;
}

char _license[] SEC("license") = "Dual MIT/GPL";
