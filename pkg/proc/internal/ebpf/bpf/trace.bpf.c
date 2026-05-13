#include "include/trace.bpf.h"

// get_value_from_register copies one general-purpose register value from the
// pre-captured regs[] array into dest. regs[] is PTR_TO_STACK — the verifier
// accepts indexed stack access unlike CTX pointer arithmetic.
__always_inline
void get_value_from_register(u64 *regs, void *dest, int reg_num) {
    u64 val = 0;
    switch (reg_num) {
    case 0:  val = regs[0];  break; // RAX
    case 1:  val = regs[1];  break; // RDX
    case 2:  val = regs[2];  break; // RCX
    case 3:  val = regs[3];  break; // RBX
    case 4:  val = regs[4];  break; // RSI
    case 5:  val = regs[5];  break; // RDI
    case 6:  val = regs[6];  break; // RBP
    case 7:  val = regs[7];  break; // RSP
    case 8:  val = regs[8];  break; // R8
    case 9:  val = regs[9];  break; // R9
    case 10: val = regs[10]; break; // R10
    case 11: val = regs[11]; break; // R11
    case 12: val = regs[12]; break; // R12
    case 13: val = regs[13]; break; // R13
    case 14: val = regs[14]; break; // R14
    case 15: val = regs[15]; break; // R15
    }
    __builtin_memcpy(dest, &val, sizeof(val));
}

// read_register_value reads a parameter value from the regs[] snapshot into
// dest. Handles up to 6 register pieces (48 bytes max).
__always_inline
int read_register_value(u64 *regs, function_parameter_t *param, char *dest) {
    switch (param->n_pieces) {
    case 6:
        get_value_from_register(regs, dest+40, param->reg_nums[5]);
    case 5:
        get_value_from_register(regs, dest+32, param->reg_nums[4]);
    case 4:
        get_value_from_register(regs, dest+24, param->reg_nums[3]);
    case 3:
        get_value_from_register(regs, dest+16, param->reg_nums[2]);
    case 2:
        get_value_from_register(regs, dest+8, param->reg_nums[1]);
    case 1:
        get_value_from_register(regs, dest, param->reg_nums[0]);
    }
    return 0;
}

// read_stack_value reads a parameter value from the stack into dest.
__always_inline
int read_stack_value(struct pt_regs *ctx, function_parameter_t *param, char *dest) {
    size_t addr = ctx->sp + param->offset;
    u32 sz = param->val_size;
    if (sz > MAX_VAL_SIZE)
        sz = MAX_VAL_SIZE;
    bpf_probe_read_user(dest, sz, (void *)(addr));
    return 0;
}

// WRITE_DEREF_ENTRY writes one dereference result into the scratch buffer
// at a compile-time-constant offset within the parameter's data region.
// J must be a compile-time constant integer literal.
#define WRITE_DEREF_ENTRY(param, val_start, scratch, J)                     \
do {                                                                        \
    if ((J) >= (int)(param)->n_derefs) break;                               \
    unsigned int _de_off = (param)->derefs[J].offset;                       \
    u64 _de_ptr = 0;                                                        \
    bpf_probe_read_kernel(&_de_ptr, sizeof(u64), val_start + _de_off);     \
    char *_de_dst = (scratch) + sizeof(param_event_t) + MAX_VAL_SIZE        \
                    + (J) * DEREF_ENTRY_SIZE;                               \
    if (_de_ptr == 0)                                                       \
        bpf_probe_read_kernel(_de_dst, DEREF_ENTRY_SIZE, NULL);            \
    else                                                                     \
        bpf_probe_read_user(_de_dst, DEREF_ENTRY_SIZE, (void *)_de_ptr);   \
} while (0)

// WRITE_AND_OUTPUT_PARAM builds a PARAM event in the scratch buffer and
// outputs it to the ring buffer. The scratch buffer is written at fixed
// offsets (verifier-safe), then bpf_ringbuf_output copies only the used
// bytes (runtime-bounded size).
#define WRITE_AND_OUTPUT_PARAM(regs, ctx, param, scratch, goid, fnaddr, idx, isret) \
do {                                                                        \
    /* Write param event header. */                                         \
    param_event_t *_pe = (param_event_t *)(scratch);                        \
    _pe->type = EVENT_TYPE_PARAM;                                           \
    _pe->goroutine_id = (goid);                                             \
    _pe->fn_addr = (fnaddr);                                                \
    _pe->param_idx = (idx);                                                 \
    _pe->is_ret = (isret);                                                  \
    _pe->kind = (param)->kind;                                              \
    _pe->val_size = (param)->val_size;                                      \
    _pe->n_derefs = (param)->n_derefs;                                      \
    _pe->deref_sizes[0] = (param)->derefs[0].size;                          \
    _pe->deref_sizes[1] = (param)->derefs[1].size;                          \
    _pe->deref_sizes[2] = (param)->derefs[2].size;                          \
    _pe->deref_sizes[3] = (param)->derefs[3].size;                          \
    _pe->deref_sizes[4] = (param)->derefs[4].size;                          \
    _pe->deref_sizes[5] = (param)->derefs[5].size;                          \
    _pe->deref_sizes[6] = (param)->derefs[6].size;                          \
    _pe->deref_sizes[7] = (param)->derefs[7].size;                          \
    /* Write val data at fixed offset after param_event_t. */               \
    char *_val = (scratch) + sizeof(param_event_t);                         \
    if ((param)->val_size > 0 && (param)->val_size <= MAX_VAL_SIZE) {       \
        if ((param)->in_reg)                                                \
            read_register_value(regs, param, _val);                         \
        else                                                                \
            read_stack_value(ctx, param, _val);                             \
    }                                                                       \
    /* Execute dereference plan. */                                         \
    if ((param)->n_derefs > 0) {                                            \
        WRITE_DEREF_ENTRY(param, _val, scratch, 0);                         \
        WRITE_DEREF_ENTRY(param, _val, scratch, 1);                         \
        WRITE_DEREF_ENTRY(param, _val, scratch, 2);                         \
        WRITE_DEREF_ENTRY(param, _val, scratch, 3);                         \
        WRITE_DEREF_ENTRY(param, _val, scratch, 4);                         \
        WRITE_DEREF_ENTRY(param, _val, scratch, 5);                         \
        WRITE_DEREF_ENTRY(param, _val, scratch, 6);                         \
        WRITE_DEREF_ENTRY(param, _val, scratch, 7);                         \
    }                                                                       \
    /* Compute actual event size and output to ring buffer.                 \
     * Deref data lives at fixed offsets (sizeof(param_event_t) +           \
     * MAX_VAL_SIZE + J * DEREF_ENTRY_SIZE) in the scratch buffer,          \
     * so the output must include the full val region when derefs exist. */ \
    u32 _evt_sz;                                                            \
    if ((param)->n_derefs > 0)                                              \
        _evt_sz = sizeof(param_event_t) + MAX_VAL_SIZE                      \
                  + (param)->n_derefs * DEREF_ENTRY_SIZE;                   \
    else                                                                    \
        _evt_sz = sizeof(param_event_t) + (param)->val_size;                \
    if (_evt_sz > SCRATCH_SIZE) _evt_sz = SCRATCH_SIZE;                     \
    bpf_ringbuf_output(&events, scratch, _evt_sz, 0);                     \
} while (0)

SEC("uprobe/dlv_trace")
int uprobe__dlv_trace(struct pt_regs *ctx) {
    function_parameter_list_t *args;
    uint64_t key = ctx->ip;

    args = bpf_map_lookup_elem(&arg_map, &key);
    if (!args)
        return 1;

    // Snapshot registers.
    u64 regs[16];
    regs[0]  = ctx->ax;  regs[1]  = ctx->dx;
    regs[2]  = ctx->cx;  regs[3]  = ctx->bx;
    regs[4]  = ctx->si;  regs[5]  = ctx->di;
    regs[6]  = ctx->bp;  regs[7]  = ctx->sp;
    regs[8]  = ctx->r8;  regs[9]  = ctx->r9;
    regs[10] = ctx->r10; regs[11] = ctx->r11;
    regs[12] = ctx->r12; regs[13] = ctx->r13;
    regs[14] = ctx->r14; regs[15] = ctx->r15;

    // Get goroutine ID.
    struct task_struct *task = (struct task_struct *)bpf_get_current_task();
    size_t g_addr;
    bpf_probe_read_user(&g_addr, sizeof(void *),
        (void*)(BPF_CORE_READ(task, thread.fsbase) + args->g_addr_offset));
    long long goid = 0;
    bpf_probe_read_user(&goid, sizeof(long long), (void*)(g_addr + args->goid_offset));

    // Emit HEADER event.
    event_header_t hdr = {};
    hdr.type = EVENT_TYPE_HEADER;
    hdr.goroutine_id = goid;
    hdr.fn_addr = args->fn_addr;
    hdr.is_ret = args->is_ret;
    hdr.n_params = args->is_ret ? args->n_ret_parameters : args->n_parameters;
    bpf_ringbuf_output(&events, &hdr, sizeof(hdr), BPF_RB_FORCE_WAKEUP);

    // Look up scratch buffer.
    u32 zero = 0;
    char *sbuf = bpf_map_lookup_elem(&scratch, &zero);
    if (!sbuf)
        return 1;

    // Emit PARAM events.
    if (!args->is_ret) {
        if (args->n_parameters >= 1) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->params[0], sbuf, goid, args->fn_addr, 0, false);
        if (args->n_parameters >= 2) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->params[1], sbuf, goid, args->fn_addr, 1, false);
        if (args->n_parameters >= 3) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->params[2], sbuf, goid, args->fn_addr, 2, false);
        if (args->n_parameters >= 4) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->params[3], sbuf, goid, args->fn_addr, 3, false);
        if (args->n_parameters >= 5) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->params[4], sbuf, goid, args->fn_addr, 4, false);
        if (args->n_parameters >= 6) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->params[5], sbuf, goid, args->fn_addr, 5, false);
    } else {
        if (args->n_ret_parameters >= 1) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->ret_params[0], sbuf, goid, args->fn_addr, 0, true);
        if (args->n_ret_parameters >= 2) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->ret_params[1], sbuf, goid, args->fn_addr, 1, true);
        if (args->n_ret_parameters >= 3) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->ret_params[2], sbuf, goid, args->fn_addr, 2, true);
        if (args->n_ret_parameters >= 4) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->ret_params[3], sbuf, goid, args->fn_addr, 3, true);
        if (args->n_ret_parameters >= 5) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->ret_params[4], sbuf, goid, args->fn_addr, 4, true);
        if (args->n_ret_parameters >= 6) WRITE_AND_OUTPUT_PARAM(regs, ctx, &args->ret_params[5], sbuf, goid, args->fn_addr, 5, true);
    }

    return 0;
}

char _license[] SEC("license") = "Dual MIT/GPL";
