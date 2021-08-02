#include "trace.bpf.h"
#include <string.h>

#define STRING_KIND 24

// parse_string_param will parse a string parameter. The parsed value of the string
// will be put into param->deref_val. This function expects the string struct
// which contains a pointer to the string and the length of the string to have
// already been read from memory and passed in as param->val.
__always_inline
int parse_string_param(struct pt_regs *ctx, function_parameter_t *param) {
    u64 str_len;
    size_t str_addr;

    memcpy(&str_addr, param->val, sizeof(str_addr));
    memcpy(&str_len, param->val + sizeof(str_addr), sizeof(str_len));
    param->daddr = str_addr;

    if (str_addr != 0) {
        if (str_len > 0x30) {
            str_len = 0x30;
        }
        int ret = bpf_probe_read_user(&param->deref_val, str_len, (void *)(str_addr));
        if (ret < 0) {
            return 1;
        }
    }
    return 0;
}

__always_inline
int parse_param_stack(struct pt_regs *ctx, function_parameter_t *param) {
    long ret;
    size_t addr = ctx->sp + param->offset;
    ret = bpf_probe_read_user(&param->val, param->size, (void *)(addr));
    if (ret < 0) {
        return 1;
    }
    return 0;
}

__always_inline
void get_value_from_register(struct pt_regs *ctx, void *dest, int reg_num) {
    switch (reg_num) {
        case 0: // RAX
            memcpy(dest, &ctx->ax, sizeof(ctx->ax));
            break;
        case 1: // RDX
            memcpy(dest, &ctx->dx, sizeof(ctx->dx));
            break;
        case 2: // RCX
            memcpy(dest, &ctx->cx, sizeof(ctx->cx));
            break;
        case 3: // RBX
            memcpy(dest, &ctx->bx, sizeof(ctx->bx));
            break;
        case 4: // RSI
            memcpy(dest, &ctx->si, sizeof(ctx->si));
            break;
        case 5: // RDI
            memcpy(dest, &ctx->di, sizeof(ctx->di));
            break;
        case 6: // RBP
            memcpy(dest, &ctx->bp, sizeof(ctx->bp));
            break;
        case 7: // RSP
            memcpy(dest, &ctx->sp, sizeof(ctx->sp));
            break;
        case 8: // R8
            memcpy(dest, &ctx->r8, sizeof(ctx->r8));
            break;
        case 9: // R9
            memcpy(dest, &ctx->r9, sizeof(ctx->r9));
            break;
        case 10: // R10
            memcpy(dest, &ctx->r10, sizeof(ctx->r10));
            break;
        case 11: // R11
            memcpy(dest, &ctx->r11, sizeof(ctx->r11));
            break;
        case 12: // R12
            memcpy(dest, &ctx->r12, sizeof(ctx->r12));
            break;
        case 13: // R13
            memcpy(dest, &ctx->r13, sizeof(ctx->r13));
            break;
        case 14: // R14
            memcpy(dest, &ctx->r14, sizeof(ctx->r14));
            break;
        case 15: // R15
            memcpy(dest, &ctx->r15, sizeof(ctx->r15));
            break;
    }
}

__always_inline
int parse_param_registers(struct pt_regs *ctx, function_parameter_t *param) {
    switch (param->n_pieces) {
        case 6:
            get_value_from_register(ctx, param->val+40, param->reg_nums[5]);
        case 5:
            get_value_from_register(ctx, param->val+32, param->reg_nums[4]);
        case 4:
            get_value_from_register(ctx, param->val+24, param->reg_nums[3]);
        case 3:
            get_value_from_register(ctx, param->val+16, param->reg_nums[2]);
        case 2:
            get_value_from_register(ctx, param->val+8, param->reg_nums[1]);
        case 1:
            get_value_from_register(ctx, param->val, param->reg_nums[0]);
    }
    return 0;
}

__always_inline
int parse_param(struct pt_regs *ctx, function_parameter_t *param) {
    if (param->size > 0x30) {
        return 0;
    }

    // Parse the initial value of the parameter.
    // If the parameter is a basic type, we will be finished here.
    // If the parameter is a more complex type such as a string or
    // a slice we will need some further processing below.
    int ret = 0;
    if (param->in_reg) {
        parse_param_registers(ctx, param);
    } else {
        parse_param_stack(ctx, param);
    }
    if (ret != 0) {
        return ret;
    }

    switch (param->kind) {
        case STRING_KIND:
            return parse_string_param(ctx, param);
    }

    return 0;
}

SEC("uprobe/dlv_trace")
int uprobe__dlv_trace(struct pt_regs *ctx) {
    function_parameter_list_t *args;
    function_parameter_list_t *parsed_args;
    function_parameter_t param;
    uint64_t key = ctx->ip;

    args = bpf_map_lookup_elem(&arg_map, &key);
    if (!args) {
        return 1;
    }

    parsed_args = bpf_ringbuf_reserve(&events, sizeof(function_parameter_list_t), 0); 
    if (!parsed_args) {
        return 1;
    }
    memcpy(parsed_args, args, sizeof(function_parameter_list_t));

    // Since we cannot loop in eBPF programs let's take adavantage of the
    // fact that in C switch cases will pass through automatically.
    switch (args->n_parameters) {
        case 6:
            parse_param(ctx, &parsed_args->params[5]);
        case 5:
            parse_param(ctx, &parsed_args->params[4]);
        case 4:
            parse_param(ctx, &parsed_args->params[3]);
        case 3:
            parse_param(ctx, &parsed_args->params[2]);
        case 2:
            parse_param(ctx, &parsed_args->params[1]);
        case 1:
            parse_param(ctx, &parsed_args->params[0]);
    }

    bpf_ringbuf_submit(parsed_args, 0);

    return 0;
}

char _license[] SEC("license") = "GPL";