//+build linux

package sameuser

import (
	"net"
	"testing"
)

func TestSameUserForRemoteAddr(t *testing.T) {
	uid = 149098
	var proc string
	readFile = func(string) ([]byte, error) {
		return []byte(proc), nil
	}
	for _, tt := range []struct {
		name string
		proc string
		addr *net.TCPAddr
		want bool
	}{
		{
			name: "ipv4-same",
			proc: `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
  21: 0100007F:E682 0100007F:0FC8 01 00000000:00000000 00:00000000 00000000 149098        0 8420541 2 0000000000000000 20 0 0 10 -1                  `,
			addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 59010},
			want: true,
		},

		{
			name: "ipv4-not-found",
			proc: `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
  21: 0100007F:E682 0100007F:0FC8 01 00000000:00000000 00:00000000 00000000 149098        0 8420541 2 0000000000000000 20 0 0 10 -1                  `,
			addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2342},
			want: false,
		},

		{
			name: "ipv4-different-uid",
			proc: `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
  21: 0100007F:E682 0100007F:0FC8 01 00000000:00000000 00:00000000 00000000 149097        0 8420541 2 0000000000000000 20 0 0 10 -1                  `,
			addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 59010},
			want: false,
		},

		{
			name: "ipv6-same",
			proc: `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   5: 00000000000000000000000001000000:D3E4 00000000000000000000000001000000:0FC8 01 00000000:00000000 00:00000000 00000000 149098        0 8425526 2 0000000000000000 20 0 0 10 -1
   6: 00000000000000000000000001000000:0FC8 00000000000000000000000001000000:D3E4 01 00000000:00000000 00:00000000 00000000 149098        0 8424744 1 0000000000000000 20 0 0 10 -1`,
			addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 54244},
			want: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			proc = tt.proc
			// The returned error is for reporting only.
			same, _ := sameUserForRemoteAddr(tt.addr)
			if got, want := same, tt.want; got != want {
				t.Errorf("sameUserForRemoteAddr(%v) = %v, want %v", tt.addr, got, want)
			}
		})
	}
}
