//go:build !linux

package sameuser

import "net"

func CanAccept(_, _, _ net.Addr) bool {
	return true
}
