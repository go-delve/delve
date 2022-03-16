//go:build !linux
// +build !linux

package native

import "errors"

// FollowExec enables (or disables) follow exec mode
func (*nativeProcess) FollowExec(bool) error {
	return errors.New("follow exec not implemented")
}
