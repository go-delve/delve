// cgo -godefs types_freebsd.go | go run mkpost.go
// Code generated by the command above; see README.md. DO NOT EDIT.

// +build amd64,freebsd

package unix

const (
	sizeofPtr      = 0x8
	sizeofShort    = 0x2
	sizeofInt      = 0x4
	sizeofLong     = 0x8
	sizeofLongLong = 0x8
)

type (
	_C_short     int16
	_C_int       int32
	_C_long      int64
	_C_long_long int64
)

type Timespec struct {
	Sec  int64
	Nsec int64
}

type Timeval struct {
	Sec  int64
	Usec int64
}

type Rusage struct {
	Utime    Timeval
	Stime    Timeval
	Maxrss   int64
	Ixrss    int64
	Idrss    int64
	Isrss    int64
	Minflt   int64
	Majflt   int64
	Nswap    int64
	Inblock  int64
	Oublock  int64
	Msgsnd   int64
	Msgrcv   int64
	Nsignals int64
	Nvcsw    int64
	Nivcsw   int64
}

type Rlimit struct {
	Cur int64
	Max int64
}

type _Gid_t uint32

const (
	S_IFMT   = 0xf000
	S_IFIFO  = 0x1000
	S_IFCHR  = 0x2000
	S_IFDIR  = 0x4000
	S_IFBLK  = 0x6000
	S_IFREG  = 0x8000
	S_IFLNK  = 0xa000
	S_IFSOCK = 0xc000
	S_ISUID  = 0x800
	S_ISGID  = 0x400
	S_ISVTX  = 0x200
	S_IRUSR  = 0x100
	S_IWUSR  = 0x80
	S_IXUSR  = 0x40
)

type Stat_t struct {
	Dev           uint64
	Ino           uint64
	Mode          uint16
	Nlink         uint64
	Uid           uint32
	Gid           uint32
	Rdev          uint64
	Atimespec     Timespec
	Mtimespec     Timespec
	Ctimespec     Timespec
	Size          int64
	Blocks        int64
	Blksize       int32
	Flags         uint32
	Gen           uint32
	Lspare        int32
	Birthtimespec Timespec
}

type Statfs_t struct {
	Version     uint32
	Type        uint32
	Flags       uint64
	Bsize       uint64
	Iosize      uint64
	Blocks      uint64
	Bfree       uint64
	Bavail      int64
	Files       uint64
	Ffree       int64
	Syncwrites  uint64
	Asyncwrites uint64
	Syncreads   uint64
	Asyncreads  uint64
	Spare       [10]uint64
	Namemax     uint32
	Owner       uint32
	Fsid        Fsid
	Charspare   [80]int8
	Fstypename  [16]int8
	Mntfromname [1024]int8
	Mntonname   [1024]int8
}

type Flock_t struct {
	Start  int64
	Len    int64
	Pid    int32
	Type   int16
	Whence int16
	Sysid  int32
	_      [4]byte
}

type Dirent struct {
	Fileno uint64
	Off    int64
	Reclen uint16
	Type   uint8
	Pad0   uint8
	Namlen uint16
	Pad1   uint16
	Name   [256]int8
}

type Fsid struct {
	Val [2]int32
}

const (
	PathMax = 0x400
)

const (
	FADV_NORMAL     = 0x0
	FADV_RANDOM     = 0x1
	FADV_SEQUENTIAL = 0x2
	FADV_WILLNEED   = 0x3
	FADV_DONTNEED   = 0x4
	FADV_NOREUSE    = 0x5
)

type RawSockaddrInet4 struct {
	Len    uint8
	Family uint8
	Port   uint16
	Addr   [4]byte /* in_addr */
	Zero   [8]int8
}

type RawSockaddrInet6 struct {
	Len      uint8
	Family   uint8
	Port     uint16
	Flowinfo uint32
	Addr     [16]byte /* in6_addr */
	Scope_id uint32
}

type RawSockaddrUnix struct {
	Len    uint8
	Family uint8
	Path   [104]int8
}

type RawSockaddrDatalink struct {
	Len    uint8
	Family uint8
	Index  uint16
	Type   uint8
	Nlen   uint8
	Alen   uint8
	Slen   uint8
	Data   [46]int8
}

type RawSockaddr struct {
	Len    uint8
	Family uint8
	Data   [14]int8
}

type RawSockaddrAny struct {
	Addr RawSockaddr
	Pad  [92]int8
}

type _Socklen uint32

type Linger struct {
	Onoff  int32
	Linger int32
}

type Iovec struct {
	Base *byte
	Len  uint64
}

type IPMreq struct {
	Multiaddr [4]byte /* in_addr */
	Interface [4]byte /* in_addr */
}

type IPMreqn struct {
	Multiaddr [4]byte /* in_addr */
	Address   [4]byte /* in_addr */
	Ifindex   int32
}

type IPv6Mreq struct {
	Multiaddr [16]byte /* in6_addr */
	Interface uint32
}

type Msghdr struct {
	Name       *byte
	Namelen    uint32
	Iov        *Iovec
	Iovlen     int32
	Control    *byte
	Controllen uint32
	Flags      int32
}

type Cmsghdr struct {
	Len   uint32
	Level int32
	Type  int32
}

type Inet6Pktinfo struct {
	Addr    [16]byte /* in6_addr */
	Ifindex uint32
}

type IPv6MTUInfo struct {
	Addr RawSockaddrInet6
	Mtu  uint32
}

type ICMPv6Filter struct {
	Filt [8]uint32
}

const (
	SizeofSockaddrInet4    = 0x10
	SizeofSockaddrInet6    = 0x1c
	SizeofSockaddrAny      = 0x6c
	SizeofSockaddrUnix     = 0x6a
	SizeofSockaddrDatalink = 0x36
	SizeofLinger           = 0x8
	SizeofIPMreq           = 0x8
	SizeofIPMreqn          = 0xc
	SizeofIPv6Mreq         = 0x14
	SizeofMsghdr           = 0x30
	SizeofCmsghdr          = 0xc
	SizeofInet6Pktinfo     = 0x14
	SizeofIPv6MTUInfo      = 0x20
	SizeofICMPv6Filter     = 0x20
)

const (
	PTRACE_ATTACH     = 0xa
	PTRACE_CONT       = 0x7
	PTRACE_DETACH     = 0xb
	PTRACE_GETFPREGS  = 0x23
	PTRACE_GETFSBASE  = 0x47
	PTRACE_GETLWPLIST = 0xf
	PTRACE_GETNUMLWPS = 0xe
	PTRACE_GETREGS    = 0x21
	PTRACE_GETXSTATE  = 0x45
	PTRACE_IO         = 0xc
	PTRACE_KILL       = 0x8
	PTRACE_LWPEVENTS  = 0x18
	PTRACE_LWPINFO    = 0xd
	PTRACE_SETFPREGS  = 0x24
	PTRACE_SETREGS    = 0x22
	PTRACE_SINGLESTEP = 0x9
	PTRACE_TRACEME    = 0x0
)

const (
	PIOD_READ_D  = 0x1
	PIOD_WRITE_D = 0x2
	PIOD_READ_I  = 0x3
	PIOD_WRITE_I = 0x4
)

const (
	PL_FLAG_BORN   = 0x100
	PL_FLAG_EXITED = 0x200
	PL_FLAG_SI     = 0x20
)

const (
	TRAP_BRKPT = 0x1
	TRAP_TRACE = 0x2
)

type PtraceLwpInfoStruct struct {
	Lwpid        int32
	Event        int32
	Flags        int32
	Sigmask      Sigset_t
	Siglist      Sigset_t
	Siginfo      __Siginfo
	Tdname       [20]int8
	Child_pid    int32
	Syscall_code uint32
	Syscall_narg uint32
}

type __Siginfo struct {
	Signo  int32
	Errno  int32
	Code   int32
	Pid    int32
	Uid    uint32
	Status int32
	Addr   *byte
	Value  [8]byte
	_      [40]byte
}

type Sigset_t struct {
	_ [4]uint32
}

type Reg struct {
	R15    int64
	R14    int64
	R13    int64
	R12    int64
	R11    int64
	R10    int64
	R9     int64
	R8     int64
	Rdi    int64
	Rsi    int64
	Rbp    int64
	Rbx    int64
	Rdx    int64
	Rcx    int64
	Rax    int64
	Trapno uint32
	Fs     uint16
	Gs     uint16
	Err    uint32
	Es     uint16
	Ds     uint16
	Rip    int64
	Cs     int64
	Rflags int64
	Rsp    int64
	Ss     int64
}

type FpReg struct {
	Env   [4]uint64
	Acc   [8][16]uint8
	Xacc  [16][16]uint8
	Spare [12]uint64
}

type PtraceIoDesc struct {
	Op   int32
	Offs *byte
	Addr *byte
	Len  uint64
}

type Kevent_t struct {
	Ident  uint64
	Filter int16
	Flags  uint16
	Fflags uint32
	Data   int64
	Udata  *byte
	Ext    [4]uint64
}

type FdSet struct {
	_ [16]uint64
}

const (
	sizeofIfMsghdr         = 0xa8
	SizeofIfMsghdr         = 0xa8
	sizeofIfData           = 0x98
	SizeofIfData           = 0x98
	SizeofIfaMsghdr        = 0x14
	SizeofIfmaMsghdr       = 0x10
	SizeofIfAnnounceMsghdr = 0x18
	SizeofRtMsghdr         = 0x98
	SizeofRtMetrics        = 0x70
)

type ifMsghdr struct {
	Msglen  uint16
	Version uint8
	Type    uint8
	Addrs   int32
	Flags   int32
	Index   uint16
	_       uint16
	Data    ifData
}

type IfMsghdr struct {
	Msglen  uint16
	Version uint8
	Type    uint8
	Addrs   int32
	Flags   int32
	Index   uint16
	Data    IfData
}

type ifData struct {
	Type       uint8
	Physical   uint8
	Addrlen    uint8
	Hdrlen     uint8
	Link_state uint8
	Vhid       uint8
	Datalen    uint16
	Mtu        uint32
	Metric     uint32
	Baudrate   uint64
	Ipackets   uint64
	Ierrors    uint64
	Opackets   uint64
	Oerrors    uint64
	Collisions uint64
	Ibytes     uint64
	Obytes     uint64
	Imcasts    uint64
	Omcasts    uint64
	Iqdrops    uint64
	Oqdrops    uint64
	Noproto    uint64
	Hwassist   uint64
	_          [8]byte
	_          [16]byte
}

type IfData struct {
	Type        uint8
	Physical    uint8
	Addrlen     uint8
	Hdrlen      uint8
	Link_state  uint8
	Spare_char1 uint8
	Spare_char2 uint8
	Datalen     uint8
	Mtu         uint64
	Metric      uint64
	Baudrate    uint64
	Ipackets    uint64
	Ierrors     uint64
	Opackets    uint64
	Oerrors     uint64
	Collisions  uint64
	Ibytes      uint64
	Obytes      uint64
	Imcasts     uint64
	Omcasts     uint64
	Iqdrops     uint64
	Noproto     uint64
	Hwassist    uint64
	Epoch       int64
	Lastchange  Timeval
}

type IfaMsghdr struct {
	Msglen  uint16
	Version uint8
	Type    uint8
	Addrs   int32
	Flags   int32
	Index   uint16
	_       uint16
	Metric  int32
}

type IfmaMsghdr struct {
	Msglen  uint16
	Version uint8
	Type    uint8
	Addrs   int32
	Flags   int32
	Index   uint16
	_       uint16
}

type IfAnnounceMsghdr struct {
	Msglen  uint16
	Version uint8
	Type    uint8
	Index   uint16
	Name    [16]int8
	What    uint16
}

type RtMsghdr struct {
	Msglen  uint16
	Version uint8
	Type    uint8
	Index   uint16
	_       uint16
	Flags   int32
	Addrs   int32
	Pid     int32
	Seq     int32
	Errno   int32
	Fmask   int32
	Inits   uint64
	Rmx     RtMetrics
}

type RtMetrics struct {
	Locks    uint64
	Mtu      uint64
	Hopcount uint64
	Expire   uint64
	Recvpipe uint64
	Sendpipe uint64
	Ssthresh uint64
	Rtt      uint64
	Rttvar   uint64
	Pksent   uint64
	Weight   uint64
	Filler   [3]uint64
}

const (
	SizeofBpfVersion    = 0x4
	SizeofBpfStat       = 0x8
	SizeofBpfZbuf       = 0x18
	SizeofBpfProgram    = 0x10
	SizeofBpfInsn       = 0x8
	SizeofBpfHdr        = 0x20
	SizeofBpfZbufHeader = 0x20
)

type BpfVersion struct {
	Major uint16
	Minor uint16
}

type BpfStat struct {
	Recv uint32
	Drop uint32
}

type BpfZbuf struct {
	Bufa   *byte
	Bufb   *byte
	Buflen uint64
}

type BpfProgram struct {
	Len   uint32
	Insns *BpfInsn
}

type BpfInsn struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

type BpfHdr struct {
	Tstamp  Timeval
	Caplen  uint32
	Datalen uint32
	Hdrlen  uint16
	_       [6]byte
}

type BpfZbufHeader struct {
	Kernel_gen uint32
	Kernel_len uint32
	User_gen   uint32
	_          [5]uint32
}

type Termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Cc     [20]uint8
	Ispeed uint32
	Ospeed uint32
}

type Winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

const (
	AT_FDCWD            = -0x64
	AT_REMOVEDIR        = 0x800
	AT_SYMLINK_FOLLOW   = 0x400
	AT_SYMLINK_NOFOLLOW = 0x200
)

type PollFd struct {
	Fd      int32
	Events  int16
	Revents int16
}

const (
	POLLERR      = 0x8
	POLLHUP      = 0x10
	POLLIN       = 0x1
	POLLINIGNEOF = 0x2000
	POLLNVAL     = 0x20
	POLLOUT      = 0x4
	POLLPRI      = 0x2
	POLLRDBAND   = 0x80
	POLLRDNORM   = 0x40
	POLLWRBAND   = 0x100
	POLLWRNORM   = 0x4
)

type CapRights struct {
	Rights [2]uint64
}

type Utsname struct {
	Sysname  [256]byte
	Nodename [256]byte
	Release  [256]byte
	Version  [256]byte
	Machine  [256]byte
}
