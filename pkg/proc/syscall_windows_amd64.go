package proc

import "unsafe"

const (
	_CONTEXT_AMD64               = 0x100000
	_CONTEXT_CONTROL             = (_CONTEXT_AMD64 | 0x1)
	_CONTEXT_INTEGER             = (_CONTEXT_AMD64 | 0x2)
	_CONTEXT_SEGMENTS            = (_CONTEXT_AMD64 | 0x4)
	_CONTEXT_FLOATING_POINT      = (_CONTEXT_AMD64 | 0x8)
	_CONTEXT_DEBUG_REGISTERS     = (_CONTEXT_AMD64 | 0x10)
	_CONTEXT_FULL                = (_CONTEXT_CONTROL | _CONTEXT_INTEGER | _CONTEXT_FLOATING_POINT)
	_CONTEXT_ALL                 = (_CONTEXT_CONTROL | _CONTEXT_INTEGER | _CONTEXT_SEGMENTS | _CONTEXT_FLOATING_POINT | _CONTEXT_DEBUG_REGISTERS)
	_CONTEXT_EXCEPTION_ACTIVE    = 0x8000000
	_CONTEXT_SERVICE_ACTIVE      = 0x10000000
	_CONTEXT_EXCEPTION_REQUEST   = 0x40000000
	_CONTEXT_EXCEPTION_REPORTING = 0x80000000
)

type _M128A struct {
	Low  uint64
	High int64
}

type _XMM_SAVE_AREA32 struct {
	ControlWord    uint16
	StatusWord     uint16
	TagWord        byte
	Reserved1      byte
	ErrorOpcode    uint16
	ErrorOffset    uint32
	ErrorSelector  uint16
	Reserved2      uint16
	DataOffset     uint32
	DataSelector   uint16
	Reserved3      uint16
	MxCsr          uint32
	MxCsr_Mask     uint32
	FloatRegisters [8]_M128A
	XmmRegisters   [256]byte
	Reserved4      [96]byte
}

type _CONTEXT struct {
	P1Home uint64
	P2Home uint64
	P3Home uint64
	P4Home uint64
	P5Home uint64
	P6Home uint64

	ContextFlags uint32
	MxCsr        uint32

	SegCs  uint16
	SegDs  uint16
	SegEs  uint16
	SegFs  uint16
	SegGs  uint16
	SegSs  uint16
	EFlags uint32

	Dr0 uint64
	Dr1 uint64
	Dr2 uint64
	Dr3 uint64
	Dr6 uint64
	Dr7 uint64

	Rax uint64
	Rcx uint64
	Rdx uint64
	Rbx uint64
	Rsp uint64
	Rbp uint64
	Rsi uint64
	Rdi uint64
	R8  uint64
	R9  uint64
	R10 uint64
	R11 uint64
	R12 uint64
	R13 uint64
	R14 uint64
	R15 uint64

	Rip uint64

	FltSave _XMM_SAVE_AREA32

	VectorRegister [26]_M128A
	VectorControl  uint64

	DebugControl         uint64
	LastBranchToRip      uint64
	LastBranchFromRip    uint64
	LastExceptionToRip   uint64
	LastExceptionFromRip uint64
}

// newCONTEXT allocates Windows CONTEXT structure aligned to 16 bytes.
func newCONTEXT() *_CONTEXT {
	var c *_CONTEXT
	buf := make([]byte, unsafe.Sizeof(*c)+15)
	return (*_CONTEXT)(unsafe.Pointer((uintptr(unsafe.Pointer(&buf[15]))) &^ 15))
}

type _DEBUG_EVENT struct {
	DebugEventCode uint32
	ProcessId      uint32
	ThreadId       uint32
	_              uint32 // to align Union properly
	U              [160]byte
}
