//go:build linux
// +build linux

package sameuser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strings"

	"github.com/go-delve/delve/pkg/logflags"
)

// for testing
var (
	uid      = os.Getuid()
	readFile = ioutil.ReadFile
)

type errConnectionNotFound struct {
	filename string
}

func (e *errConnectionNotFound) Error() string {
	return fmt.Sprintf("connection not found in %s", e.filename)
}

func sameUserForHexLocalAddr(filename, localAddr, remoteAddr string) (bool, error) {
	b, err := readFile(filename)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		// The format contains whitespace padding (%4d, %5u), so we use
		// fmt.Sscanf instead of splitting on whitespace.
		var (
			sl                            int
			readLocalAddr, readRemoteAddr string
			state                         int
			queue, timer                  string
			retransmit                    int
			remoteUID                     uint
		)
		// Note that we must use %d where the kernel format uses %5u:
		// %u is not understood by the fmt package (%U is something else),
		// %5d cuts off longer uids (e.g. 149098 on gLinux).
		n, err := fmt.Sscanf(line, "%4d: %s %s %02X %s %s %08X %d",
			&sl, &readLocalAddr, &readRemoteAddr, &state, &queue, &timer, &retransmit, &remoteUID)
		if n != 8 || err != nil {
			continue // invalid line (e.g. header line)
		}
		if readLocalAddr != remoteAddr || readRemoteAddr != localAddr {
			// this check is deliberately crossed, the (readLocalAddr,
			// readRemoteAddr) pair is from the point of view of the client, the
			// (localAddr, remoteAddr) is from the point of view of the server.
			continue
		}
		same := uid == int(remoteUID)
		if !same && logflags.Any() {
			log.Printf("connection from different user (remote: %d, local: %d) detected: %v", remoteUID, uid, line)
		}
		return same, nil
	}
	return false, &errConnectionNotFound{filename}
}

func addrToHex4(addr *net.TCPAddr) string {
	// For details about the format, see the kernel side implementation:
	// https://elixir.bootlin.com/linux/v5.2.2/source/net/ipv4/tcp_ipv4.c#L2375
	b := addr.IP.To4()
	return fmt.Sprintf("%02X%02X%02X%02X:%04X", b[3], b[2], b[1], b[0], addr.Port)
}

func addrToHex6(addr *net.TCPAddr) string {
	a16 := addr.IP.To16()
	// For details about the format, see the kernel side implementation:
	// https://elixir.bootlin.com/linux/v5.2.2/source/net/ipv6/tcp_ipv6.c#L1792
	words := make([]uint32, 4)
	if err := binary.Read(bytes.NewReader(a16), binary.LittleEndian, words); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%08X%08X%08X%08X:%04X", words[0], words[1], words[2], words[3], addr.Port)
}

func sameUserForRemoteAddr4(localAddr, remoteAddr *net.TCPAddr) (bool, error) {
	r, err := sameUserForHexLocalAddr("/proc/net/tcp", addrToHex4(localAddr), addrToHex4(remoteAddr))
	if _, isNotFound := err.(*errConnectionNotFound); isNotFound {
		// See Issue #1835
		r, err2 := sameUserForHexLocalAddr("/proc/net/tcp6", "0000000000000000FFFF0000"+addrToHex4(localAddr), "0000000000000000FFFF0000"+addrToHex4(remoteAddr))
		if err2 == nil {
			return r, nil
		}
	}
	return r, err
}

func sameUserForRemoteAddr6(localAddr, remoteAddr *net.TCPAddr) (bool, error) {
	return sameUserForHexLocalAddr("/proc/net/tcp6", addrToHex6(localAddr), addrToHex6(remoteAddr))
}

func sameUserForRemoteAddr(localAddr, remoteAddr *net.TCPAddr) (bool, error) {
	if remoteAddr.IP.To4() == nil {
		return sameUserForRemoteAddr6(localAddr, remoteAddr)
	}
	return sameUserForRemoteAddr4(localAddr, remoteAddr)
}

func CanAccept(listenAddr, localAddr, remoteAddr net.Addr) bool {
	laddr, ok := listenAddr.(*net.TCPAddr)
	if !ok || !laddr.IP.IsLoopback() {
		return true
	}
	remoteaAddrTCP := remoteAddr.(*net.TCPAddr)
	localAddrTCP := localAddr.(*net.TCPAddr)

	same, err := sameUserForRemoteAddr(localAddrTCP, remoteaAddrTCP)
	if err != nil {
		log.Printf("cannot check remote address: %v", err)
	}
	if !same {
		if logflags.Any() {
			log.Printf("closing connection from different user (%v): connections to localhost are only accepted from the same UNIX user for security reasons", remoteaAddrTCP)
		} else {
			fmt.Fprintf(os.Stderr, "closing connection from different user (%v): connections to localhost are only accepted from the same UNIX user for security reasons\n", remoteaAddrTCP)
		}
		return false
	}
	return true
}
