package native

import "github.com/go-delve/delve/pkg/proc"

// Attach attaches to an existing process, discovering its executable path.
func Attach(pid int, waitFor *proc.WaitFor, debugInfoDirs []string) (*proc.TargetGroup, error) {
	return AttachWithPath(pid, "", waitFor, debugInfoDirs)
}
