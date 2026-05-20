// Event types for the ring buffer protocol.
#define EVENT_TYPE_HEADER 0
#define EVENT_TYPE_PARAM  1

#define MAX_PARAMS_PER_EVENT 6
#define MAX_VAL_SIZE 0x30

// function_parameter stores information about a single parameter to a function.
typedef struct function_parameter {
    unsigned int kind;
    unsigned int size;
    int offset;
    bool in_reg;
    int n_pieces;
    int reg_nums[6];
    size_t daddr;
    char val[MAX_VAL_SIZE];
    char deref_val[MAX_VAL_SIZE];
} function_parameter_t;

// function_parameter_list holds info about the function parameters and
// stores information on up to 6 parameters. Used as the value type for arg_map.
// goroutine_id is NOT stored here; it is computed at runtime and sent
// in the event_header_t.
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

// event_header_t is emitted once per uprobe hit, before any param events.
typedef struct __attribute__((packed)) dlv_event_header {
    unsigned char type;          // EVENT_TYPE_HEADER
    long long goroutine_id;
    unsigned long long int fn_addr;
    bool is_ret;
    unsigned int n_params;
} event_header_t;

// param_event_t is the header for a single parameter event.
typedef struct __attribute__((packed)) dlv_param_event {
    unsigned char type;          // EVENT_TYPE_PARAM
    long long goroutine_id;
    unsigned long long int fn_addr;
    unsigned char param_idx;
    bool is_ret;
    unsigned int kind;
    unsigned int val_size;
} param_event_t;

// param_event_data_t: complete PARAM event for the ring buffer.
// Packed so the Go parser can read it with known offsets.
typedef struct __attribute__((packed)) dlv_param_event_data {
    param_event_t hdr;
    char val[MAX_VAL_SIZE];
    char deref_val[MAX_VAL_SIZE];
} param_event_data_t;
