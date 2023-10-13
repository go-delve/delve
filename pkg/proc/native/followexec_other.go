//go:build !linux && !windows

package native

import "errors"

// FollowExec enables (or disables) follow exec mode
func (*nativeProcess) FollowExec(bool) error {
	return errors.New("follow exec not implemented")
}

func (*processGroup) detachChild(*nativeProcess) error {
	panic("not implemented")
}
