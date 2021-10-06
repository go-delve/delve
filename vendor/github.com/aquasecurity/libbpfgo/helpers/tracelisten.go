package helpers

import (
	"bufio"
	"fmt"
	"os"
)

// TracePipeListen reads data from the trace pipe that bpf_trace_printk() writes to,
// (/sys/kernel/debug/tracing/trace_pipe).
// It writes the data to stdout. The pipe is global, so this function is not
// associated with any BPF program. It is recommended to use bpf_trace_printk()
// and this function for debug purposes only.
// This is a blocking function intended to be called from a goroutine.
func TracePipeListen() error {
	f, err := os.Open("/sys/kernel/debug/tracing/trace_pipe")
	if err != nil {
		return fmt.Errorf("failed to open trace pipe: %w", err)
	}
	defer f.Close()

	r := bufio.NewReader(f)
	b := make([]byte, 1024)

	for {
		l, err := r.Read(b)
		if err != nil {
			return fmt.Errorf("failed to read from trace pipe: %w", err)
		}

		s := string(b[:l])
		fmt.Println(s)
	}
}
