// Package debugdetect provides utilities for detecting if a program
// is running under a debugger.
//
// This is useful for applications that need to modify their behavior
// when being debugged, such as TUI applications that need to wait for
// debugger attachment before continuing startup.
//
// Example usage:
//
//	attached, err := debugdetect.IsDebuggerAttached()
//	if err != nil {
//		log.Fatalf("Failed to detect debugger: %v", err)
//	}
//	if !attached {
//		fmt.Println("Waiting for debugger...")
//		for {
//			if attached, _ := debugdetect.IsDebuggerAttached(); attached {
//				break
//			}
//			time.Sleep(100 * time.Millisecond)
//		}
//	}
//
// Supported platforms: linux, darwin, windows, freebsd
// Detects: ptrace-based debuggers (Delve, gdb, lldb, etc.)
package debugdetect
