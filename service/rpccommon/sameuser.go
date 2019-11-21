//+build !linux

package rpccommon

import "net"

func canAccept(_, _ net.Addr) bool {
	return true
}
