#define MAX_DEREFS 8
#define MAX_VAL_SIZE 8192

// DEREF_ENTRY_SIZE: fixed bytes reserved per dereference result in the ring
// buffer event. A fixed stride per deref entry allows the BPF verifier to
// bound-check writes into PTR_TO_MEM (ring buffer) using constant offsets,
// avoiding the "unbounded memory access" error that occurs when PTR_TO_MEM
// is advanced by a runtime value (de_size) and the verifier loses off_max.
// Total deref region per parameter: MAX_DEREFS * DEREF_ENTRY_SIZE = 16384 bytes.
#define DEREF_ENTRY_SIZE 2048

// deref_entry describes a single pointer dereference within a parameter.
typedef struct deref_entry {
    unsigned int offset; // byte offset within val where the pointer lives
    unsigned int size;   // bytes to read from the pointed-to address (≤ DEREF_ENTRY_SIZE)
} deref_entry_t;

// function_parameter stores information about a single parameter to a function.
typedef struct function_parameter {
    unsigned int kind;
    unsigned int size;
    int offset;
    bool in_reg;
    int n_pieces;
    int reg_nums[6];

    unsigned int n_derefs;
    deref_entry_t derefs[MAX_DEREFS];

    unsigned int val_size;         // min(size, MAX_VAL_SIZE)
    unsigned int total_deref_size; // sum of deref sizes (for Go-side use)
} function_parameter_t;

// function_parameter_list holds info about the function parameters.
typedef struct function_parameter_list {
    unsigned int goid_offset;
    long long g_addr_offset;

    unsigned long long int fn_addr;
    bool is_ret;

    unsigned int n_parameters;
    function_parameter_t params[6];

    unsigned int n_ret_parameters;
    function_parameter_t ret_params[6];

} function_parameter_list_t;

// MAX_PARAMS_PER_EVENT is the maximum number of parameters in a single event.
#define MAX_PARAMS_PER_EVENT 6

// Event type tags for the ring buffer protocol.
#define EVENT_TYPE_HEADER 0
#define EVENT_TYPE_PARAM  1

// event_header_t is the HEADER record written first for each uprobe fire.
typedef struct dlv_event_header {
    unsigned char type;
    long long goroutine_id;
    unsigned long long int fn_addr;
    bool is_ret;
    unsigned int n_params;
} __attribute__((packed)) event_header_t;

// param_event_t is the PARAM record written for each parameter.
typedef struct param_event {
    unsigned char type;
    long long goroutine_id;
    unsigned long long int fn_addr;
    unsigned char param_idx;
    bool is_ret;
    unsigned int kind;
    unsigned int val_size;
    unsigned int n_derefs;
    unsigned int deref_sizes[MAX_DEREFS];
} __attribute__((packed)) param_event_t;

// MAX_PARAM_EVENT_SIZE: upper bound for a single PARAM event.
// param_event_t header + MAX_VAL_SIZE + MAX_DEREFS * DEREF_ENTRY_SIZE
#define MAX_PARAM_EVENT_SIZE ((int)(sizeof(param_event_t)) + MAX_VAL_SIZE + MAX_DEREFS * DEREF_ENTRY_SIZE)

// SCRATCH_SIZE: total scratch buffer size. Must hold one full parameter's
// data at fixed-stride offsets for verifier-safe writes.
#define SCRATCH_SIZE MAX_PARAM_EVENT_SIZE
