package libbpfgo

/*
#cgo LDFLAGS: -lelf -lz

#include <bpf/bpf.h>
#include <bpf/libbpf.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>

#include <asm-generic/unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/perf_event.h>
#include <linux/unistd.h>
#include <string.h>
#include <unistd.h>

#ifndef MAX_ERRNO
#define MAX_ERRNO       4095

#define IS_ERR_VALUE(x) ((x) >= (unsigned long)-MAX_ERRNO)

static inline bool IS_ERR(const void *ptr) {
	return IS_ERR_VALUE((unsigned long)ptr);
}

static inline bool IS_ERR_OR_NULL(const void *ptr) {
	return !ptr || IS_ERR_VALUE((unsigned long)ptr);
}

static inline long PTR_ERR(const void *ptr) {
	return (long) ptr;
}
#endif

extern void perfCallback(void *ctx, int cpu, void *data, __u32 size);
extern void perfLostCallback(void *ctx, int cpu, __u64 cnt);

extern int ringbufferCallback(void *ctx, void *data, size_t size);

int libbpf_print_fn(enum libbpf_print_level level,
               const char *format, va_list args)
{
    if (level != LIBBPF_WARN)
        return 0;
    return vfprintf(stderr, format, args);
}

void set_print_fn() {
    libbpf_set_print(libbpf_print_fn);
}

struct ring_buffer * init_ring_buf(int map_fd, uintptr_t ctx) {
    struct ring_buffer *rb = NULL;
    rb = ring_buffer__new(map_fd, ringbufferCallback, (void*)ctx, NULL);
    if (!rb) {
        fprintf(stderr, "Failed to initialize ring buffer\n");
        return NULL;
    }
    return rb;
}

struct perf_buffer * init_perf_buf(int map_fd, int page_cnt, uintptr_t ctx) {
    struct perf_buffer_opts pb_opts = {};
    struct perf_buffer *pb = NULL;
    pb_opts.sample_cb = perfCallback;
    pb_opts.lost_cb = perfLostCallback;
    pb_opts.ctx = (void*)ctx;
    pb = perf_buffer__new(map_fd, page_cnt, &pb_opts);
    if (pb < 0) {
        fprintf(stderr, "Failed to initialize perf buffer!\n");
        return NULL;
    }
    return pb;
}

int poke_kprobe_events(bool add, const char* name, bool ret) {
    char buf[256];
    int fd, err;
    char pr;

    fd = open("/sys/kernel/debug/tracing/kprobe_events", O_WRONLY | O_APPEND, 0);
    if (fd < 0) {
        err = -errno;
        fprintf(stderr, "failed to open kprobe_events file: %d\n", err);
        return err;
    }

    pr = ret ? 'r' : 'p';

    if (add)
        snprintf(buf, sizeof(buf), "%c:kprobes/%c%s %s", pr, pr, name, name);
    else
        snprintf(buf, sizeof(buf), "-:kprobes/%c%s", pr, name);

    err = write(fd, buf, strlen(buf));
    if (err < 0) {
        err = -errno;
        fprintf(
            stderr,
            "failed to %s kprobe '%s': %d\n",
            add ? "add" : "remove",
            buf,
            err);
    }
    close(fd);
    return err >= 0 ? 0 : err;
}

int add_kprobe_event(const char* func_name, bool is_kretprobe) {
    return poke_kprobe_events(true, func_name, is_kretprobe);
}

int remove_kprobe_event(const char* func_name, bool is_kretprobe) {
    return poke_kprobe_events(false, func_name, is_kretprobe);
}

struct bpf_link* attach_kprobe_legacy(
    struct bpf_program* prog,
    const char* func_name,
    bool is_kretprobe) {
    char fname[256];
    struct perf_event_attr attr;
    struct bpf_link* link;
    int fd = -1, err, id;
    FILE* f = NULL;
    char pr;

    err = add_kprobe_event(func_name, is_kretprobe);
    if (err) {
        fprintf(stderr, "failed to create kprobe event: %d\n", err);
        return NULL;
    }

    pr = is_kretprobe ? 'r' : 'p';

    snprintf(
        fname,
        sizeof(fname),
        "/sys/kernel/debug/tracing/events/kprobes/%c%s/id",
        pr, func_name);
    f = fopen(fname, "r");
    if (!f) {
        fprintf(stderr, "failed to open kprobe id file '%s': %d\n", fname, -errno);
        goto err_out;
    }

    if (fscanf(f, "%d\n", &id) != 1) {
        fprintf(stderr, "failed to read kprobe id from '%s': %d\n", fname, -errno);
        goto err_out;
    }

    fclose(f);
    f = NULL;

    memset(&attr, 0, sizeof(attr));
    attr.size = sizeof(attr);
    attr.config = id;
    attr.type = PERF_TYPE_TRACEPOINT;
    attr.sample_period = 1;
    attr.wakeup_events = 1;

    fd = syscall(__NR_perf_event_open, &attr, -1, 0, -1, PERF_FLAG_FD_CLOEXEC);
    if (fd < 0) {
        fprintf(
            stderr,
            "failed to create perf event for kprobe ID %d: %d\n",
            id,
            -errno);
        goto err_out;
    }

    link = bpf_program__attach_perf_event(prog, fd);
    err = libbpf_get_error(link);
    if (err) {
        fprintf(stderr, "failed to attach to perf event FD %d: %d\n", fd, err);
        goto err_out;
    }

    return link;

err_out:
    if (f)
        fclose(f);
    if (fd >= 0)
        close(fd);
    remove_kprobe_event(func_name, is_kretprobe);
    return NULL;
}
*/
import "C"

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/aquasecurity/libbpfgo/helpers"
)

const (
	// Maximum number of channels (RingBuffers + PerfBuffers) supported
	maxEventChannels = 512
)

type Module struct {
	obj      *C.struct_bpf_object
	links    []*BPFLink
	perfBufs []*PerfBuffer
	ringBufs []*RingBuffer
}

type BPFMap struct {
	name   string
	bpfMap *C.struct_bpf_map
	fd     C.int
	module *Module
}

type BPFProg struct {
	name   string
	prog   *C.struct_bpf_program
	module *Module
}

type LinkType int

const (
	Tracepoint LinkType = iota
	RawTracepoint
	Kprobe
	Kretprobe
	KprobeLegacy
	KretprobeLegacy
	LSM
	PerfEvent
	Uprobe
	Uretprobe
)

type BPFLink struct {
	link      *C.struct_bpf_link
	prog      *BPFProg
	linkType  LinkType
	eventName string
}

type PerfBuffer struct {
	pb         *C.struct_perf_buffer
	bpfMap     *BPFMap
	slot       uint
	eventsChan chan []byte
	lostChan   chan uint64
	stop       chan struct{}
	closed     bool
	wg         sync.WaitGroup
}

type RingBuffer struct {
	rb     *C.struct_ring_buffer
	bpfMap *BPFMap
	slot   uint
	stop   chan struct{}
	closed bool
	wg     sync.WaitGroup
}

// BPF is using locked memory for BPF maps and various other things.
// By default, this limit is very low - increase to avoid failures
func bumpMemlockRlimit() error {
	var rLimit syscall.Rlimit
	rLimit.Max = 512 << 20 /* 512 MBs */
	rLimit.Cur = 512 << 20 /* 512 MBs */
	err := syscall.Setrlimit(C.RLIMIT_MEMLOCK, &rLimit)
	if err != nil {
		return fmt.Errorf("error setting rlimit: %v", err)
	}
	return nil
}

func errptrError(ptr unsafe.Pointer, format string, args ...interface{}) error {
	negErrno := C.PTR_ERR(ptr)
	errno := syscall.Errno(-int64(negErrno))
	if errno == 0 {
		return fmt.Errorf(format, args...)
	}

	args = append(args, errno.Error())
	return fmt.Errorf(format+": %v", args...)
}

func NewModuleFromFile(bpfObjFile string) (*Module, error) {
	C.set_print_fn()
	bumpMemlockRlimit()
	cs := C.CString(bpfObjFile)
	obj := C.bpf_object__open(cs)
	C.free(unsafe.Pointer(cs))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(obj)) {
		return nil, errptrError(unsafe.Pointer(obj), "failed to open BPF object %s", bpfObjFile)
	}

	return &Module{
		obj: obj,
	}, nil
}

func NewModuleFromBuffer(bpfObjBuff []byte, bpfObjName string) (*Module, error) {
	C.set_print_fn()
	bumpMemlockRlimit()
	name := C.CString(bpfObjName)
	buffSize := C.size_t(len(bpfObjBuff))
	buffPtr := unsafe.Pointer(C.CBytes(bpfObjBuff))
	obj := C.bpf_object__open_buffer(buffPtr, buffSize, name)
	C.free(unsafe.Pointer(name))
	C.free(unsafe.Pointer(buffPtr))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(obj)) {
		return nil, errptrError(unsafe.Pointer(obj), "failed to open BPF object %s: %v", bpfObjName, bpfObjBuff[:20])
	}

	return &Module{
		obj: obj,
	}, nil
}

func (m *Module) Close() {
	for _, pb := range m.perfBufs {
		pb.Close()
	}
	for _, rb := range m.ringBufs {
		rb.Close()
	}
	for _, link := range m.links {
		C.bpf_link__destroy(link.link) // this call will remove non-legacy kprobes
		if link.linkType == KprobeLegacy {
			cs := C.CString(link.eventName)
			C.remove_kprobe_event(cs, false)
			C.free(unsafe.Pointer(cs))
		}
		if link.linkType == KretprobeLegacy {
			cs := C.CString(link.eventName)
			C.remove_kprobe_event(cs, true)
			C.free(unsafe.Pointer(cs))
		}
	}
	C.bpf_object__close(m.obj)
}

func (m *Module) BPFLoadObject() error {
	ret := C.bpf_object__load(m.obj)
	if ret != 0 {
		return fmt.Errorf("failed to load BPF object")
	}

	return nil
}

func (m *Module) GetMap(mapName string) (*BPFMap, error) {
	cs := C.CString(mapName)
	bpfMap := C.bpf_object__find_map_by_name(m.obj, cs)
	C.free(unsafe.Pointer(cs))
	if bpfMap == nil {
		return nil, fmt.Errorf("failed to find BPF map %s", mapName)
	}

	return &BPFMap{
		bpfMap: bpfMap,
		name:   mapName,
		fd:     C.bpf_map__fd(bpfMap),
		module: m,
	}, nil
}

func (b *BPFMap) Pin(pinPath string) error {
	cs := C.CString(b.name)
	path := C.CString(pinPath)
	bpfMap := C.bpf_object__find_map_by_name(b.module.obj, cs)
	errC := C.bpf_map__pin(bpfMap, path)
	C.free(unsafe.Pointer(cs))
	if errC != 0 {
		return fmt.Errorf("failed to pin map %s to path %s", b.name, pinPath)
	}
	return nil
}

func (b *BPFMap) Unpin(pinPath string) error {
	cs := C.CString(b.name)
	path := C.CString(pinPath)
	bpfMap := C.bpf_object__find_map_by_name(b.module.obj, cs)
	errC := C.bpf_map__unpin(bpfMap, path)
	C.free(unsafe.Pointer(cs))
	if errC != 0 {
		return fmt.Errorf("failed to unpin map %s from path %s", b.name, pinPath)
	}
	return nil
}

func (b *BPFMap) SetPinPath(pinPath string) error {
	cs := C.CString(b.name)
	path := C.CString(pinPath)
	bpfMap := C.bpf_object__find_map_by_name(b.module.obj, cs)
	errC := C.bpf_map__set_pin_path(bpfMap, path)
	C.free(unsafe.Pointer(cs))
	if errC != 0 {
		return fmt.Errorf("failed to set pin for map %s to path %s", b.name, pinPath)
	}
	return nil
}

// Resize changes the map's capacity to maxEntries.
// It should be called after the module was initialized but
// prior to it being loaded with BPFLoadObject.
// Note: for ring buffer and perf buffer, maxEntries is the
// capacity in bytes.
func (b *BPFMap) Resize(maxEntries uint32) error {
	cs := C.CString(b.name)
	bpfMap := C.bpf_object__find_map_by_name(b.module.obj, cs)
	errC := C.bpf_map__resize(bpfMap, C.uint(maxEntries))
	C.free(unsafe.Pointer(cs))
	if errC != 0 {
		return fmt.Errorf("failed to resize map %s to %v", b.name, maxEntries)
	}
	return nil
}

// GetMaxEntries returns the map's capacity.
// Note: for ring buffer and perf buffer, maxEntries is the
// capacity in bytes.
func (b *BPFMap) GetMaxEntries() uint32 {
	cs := C.CString(b.name)
	bpfMap := C.bpf_object__find_map_by_name(b.module.obj, cs)
	maxEntries := C.bpf_map__max_entries(bpfMap)
	C.free(unsafe.Pointer(cs))
	return uint32(maxEntries)
}

func GetUnsafePointer(data interface{}) (unsafe.Pointer, error) {
	var dataPtr unsafe.Pointer
	switch k := data.(type) {
	case int8:
		dataPtr = unsafe.Pointer(&k)
	case uint8:
		dataPtr = unsafe.Pointer(&k)
	case int32:
		dataPtr = unsafe.Pointer(&k)
	case uint32:
		dataPtr = unsafe.Pointer(&k)
	case int64:
		dataPtr = unsafe.Pointer(&k)
	case uint64:
		dataPtr = unsafe.Pointer(&k)
	case []byte:
		dataPtr = unsafe.Pointer(&k[0])
	default:
		return nil, fmt.Errorf("unknown data type %T", data)
	}

	return dataPtr, nil
}

func (b *BPFMap) KeySize() int {
	return int(C.bpf_map__key_size(b.bpfMap))
}

func (b *BPFMap) ValueSize() int {
	return int(C.bpf_map__value_size(b.bpfMap))
}

// GetValue takes a pointer to the key which is stored in the map.
// It returns the associated value as a slice of bytes.
// All basic types, and structs are supported as keys.
//
// NOTE: Slices and arrays are also supported but special care
// should be taken as to take a reference to the first element
// in the slice or array instead of the slice/array itself, as to
// avoid undefined behavior.
func (b *BPFMap) GetValue(key unsafe.Pointer) ([]byte, error) {
	value := make([]byte, b.ValueSize())
	valuePtr := unsafe.Pointer(&value[0])

	errC := C.bpf_map_lookup_elem(b.fd, key, valuePtr)
	if errC != 0 {
		return nil, fmt.Errorf("failed to lookup value %v in map %s", key, b.name)
	}
	return value, nil
}

// DeleteKey takes a pointer to the key which is stored in the map.
// It removes the key and associated value from the BPFMap.
// All basic types, and structs are supported as keys.
//
// NOTE: Slices and arrays are also supported but special care
// should be taken as to take a reference to the first element
// in the slice or array instead of the slice/array itself, as to
// avoid undefined behavior.
func (b *BPFMap) DeleteKey(key unsafe.Pointer) error {
	errC := C.bpf_map_delete_elem(b.fd, key)
	if errC != 0 {
		return fmt.Errorf("failed to get lookup key %d from map %s", key, b.name)
	}
	return nil
}

// Update takes a pointer to a key and a value to associate it with in
// the BPFMap. The unsafe.Pointer should be taken on a reference to the
// underlying datatype. All basic types, and structs are supported
//
// NOTE: Slices and arrays are supported but references should be passed
// to the first element in the slice or array.
//
// For example:
//
//  key := 1
//  value := []byte{'a', 'b', 'c'}
//  keyPtr := unsafe.Pointer(&key)
//  valuePtr := unsafe.Pointer(&value[0])
//  bpfmap.Update(keyPtr, valuePtr)
//
func (b *BPFMap) Update(key, value unsafe.Pointer) error {
	errC := C.bpf_map_update_elem(b.fd, key, value, C.BPF_ANY)
	if errC != 0 {
		return fmt.Errorf("failed to update map %s", b.name)
	}
	return nil
}

type BPFMapIterator struct {
	b    *BPFMap
	err  error
	prev []byte
	next []byte
}

func (b *BPFMap) Iterator() *BPFMapIterator {
	return &BPFMapIterator{
		b:    b,
		prev: nil,
		next: nil,
	}
}

func (it *BPFMapIterator) Next() bool {
	if it.err != nil {
		return false
	}

	prevPtr := unsafe.Pointer(nil)
	if it.next != nil {
		prevPtr = unsafe.Pointer(&it.next[0])
	}

	next := make([]byte, it.b.KeySize())
	nextPtr := unsafe.Pointer(&next[0])

	errC, err := C.bpf_map_get_next_key(it.b.fd, prevPtr, nextPtr)
	if errno, ok := err.(syscall.Errno); errC == -1 && ok && errno == C.ENOENT {
		return false
	}
	if err != nil {
		it.err = err
		return false
	}

	it.prev = it.next
	it.next = next

	return true
}

// Key returns the current key value of the iterator, if the most recent call to Next returned true.
// The slice is valid only until the next call to Next.
func (it *BPFMapIterator) Key() []byte {
	return it.next
}

// Err returns the last error that ocurred while table.Iter or iter.Next
func (it *BPFMapIterator) Err() error {
	return it.err
}

func (m *Module) GetProgram(progName string) (*BPFProg, error) {
	cs := C.CString(progName)
	prog := C.bpf_object__find_program_by_name(m.obj, cs)
	C.free(unsafe.Pointer(cs))
	if prog == nil {
		return nil, fmt.Errorf("failed to find BPF program %s", progName)
	}

	return &BPFProg{
		name:   progName,
		prog:   prog,
		module: m,
	}, nil
}

func (p *BPFProg) GetFd() C.int {
	return C.bpf_program__fd(p.prog)
}

// BPFProgType is an enum as defined in https://elixir.bootlin.com/linux/latest/source/include/uapi/linux/bpf.h
type BPFProgType uint32

const (
	BPFProgTypeUnspec uint32 = iota
	BPFProgTypeSocketFilter
	BPFProgTypeKprobe
	BPFProgTypeSchedCls
	BPFProgTypeSchedAct
	BPFProgTypeTracepoint
	BPFProgTypeXdp
	BPFProgTypePerfEvent
	BPFProgTypeCgroupSkb
	BPFProgTypeCgroupSock
	BPFProgTypeLwtIn
	BPFProgTypeLwtOut
	BPFProgTypeLwtXmit
	BPFProgTypeSockOps
	BPFProgTypeSkSkb
	BPFProgTypeCgroupDevice
	BPFProgTypeSkMsg
	BPFProgTypeRawTracepoint
	BPFProgTypeCgroupSockAddr
	BPFProgTypeLwtSeg6Local
	BPFProgTypeLircMode2
	BPFProgTypeSkReuseport
	BPFProgTypeFlowDissector
	BPFProgTypeCgroupSysctl
	BPFProgTypeRawTracepointWritable
	BPFProgTypeCgroupSockopt
	BPFProgTypeTracing
	BPFProgTypeStructOps
	BPFProgTypeExt
	BPFProgTypeLsm
	BPFProgTypeSkLookup
)

func (p *BPFProg) GetType() uint32 {
	return C.bpf_program__get_type(p.prog)
}

func (p *BPFProg) SetAutoload(autoload bool) error {
	cbool := C.bool(autoload)
	err := C.bpf_program__set_autoload(p.prog, cbool)
	if err != 0 {
		return fmt.Errorf("failed to set bpf program autoload")
	}
	return nil
}

func (p *BPFProg) SetTracepoint() error {
	err := C.bpf_program__set_tracepoint(p.prog)
	if err != 0 {
		return fmt.Errorf("failed to set bpf program as tracepoint")
	}
	return nil
}

func (p *BPFProg) AttachTracepoint(tp string) (*BPFLink, error) {
	tpEvent := strings.Split(tp, ":")
	if len(tpEvent) != 2 {
		return nil, fmt.Errorf("tracepoint must be in 'category:name' format")
	}
	tpCategory := C.CString(tpEvent[0])
	tpName := C.CString(tpEvent[1])
	link := C.bpf_program__attach_tracepoint(p.prog, tpCategory, tpName)
	C.free(unsafe.Pointer(tpCategory))
	C.free(unsafe.Pointer(tpName))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach tracepoint %s to program %s", tp, p.name)
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      p,
		linkType:  Tracepoint,
		eventName: tp,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

func (p *BPFProg) AttachRawTracepoint(tpEvent string) (*BPFLink, error) {
	cs := C.CString(tpEvent)
	link := C.bpf_program__attach_raw_tracepoint(p.prog, cs)
	C.free(unsafe.Pointer(cs))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach raw tracepoint %s to program %s", tpEvent, p.name)
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      p,
		linkType:  RawTracepoint,
		eventName: tpEvent,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

func (p *BPFProg) AttachPerfEvent(fd int) (*BPFLink, error) {
	link := C.bpf_program__attach_perf_event(p.prog, C.int(fd))
	if link == nil {
		return nil, fmt.Errorf("failed to attach perf event to program %s", p.name)
	}

	bpfLink := &BPFLink{
		link:     link,
		prog:     p,
		linkType: PerfEvent,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

// this API should be used for kernels > 4.17
func (p *BPFProg) AttachKprobe(kp string) (*BPFLink, error) {
	return doAttachKprobe(p, kp, false)
}

// this API should be used for kernels > 4.17
func (p *BPFProg) AttachKretprobe(kp string) (*BPFLink, error) {
	return doAttachKprobe(p, kp, true)
}

func (p *BPFProg) AttachLSM() (*BPFLink, error) {
	link := C.bpf_program__attach_lsm(p.prog)
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach lsm to program %s", p.name)
	}

	bpfLink := &BPFLink{
		link:     link,
		prog:     p,
		linkType: LSM,
	}
	p.module.links = append(p.module.links, bpfLink)
	return bpfLink, nil
}

func doAttachKprobe(prog *BPFProg, kp string, isKretprobe bool) (*BPFLink, error) {
	cs := C.CString(kp)
	cbool := C.bool(isKretprobe)
	link := C.bpf_program__attach_kprobe(prog.prog, cbool, cs)
	C.free(unsafe.Pointer(cs))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach %s k(ret)probe to program %s", kp, prog.name)
	}

	kpType := Kprobe
	if isKretprobe {
		kpType = Kretprobe
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      prog,
		linkType:  kpType,
		eventName: kp,
	}
	prog.module.links = append(prog.module.links, bpfLink)
	return bpfLink, nil
}

// AttachUprobe attaches the BPFProgram to entry of the symbol in the library or binary at 'path'
// which can be relative or absolute. A pid can be provided to attach to, or -1 can be specified
// to attach to all processes
func (p *BPFProg) AttachUprobe(pid int, path string, offset uint32) (*BPFLink, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	return doAttachUprobe(p, false, pid, absPath, offset)
}

// AttachURetprobe attaches the BPFProgram to exit of the symbol in the library or binary at 'path'
// which can be relative or absolute. A pid can be provided to attach to, or -1 can be specified
// to attach to all processes
func (p *BPFProg) AttachURetprobe(pid int, path string, offset uint32) (*BPFLink, error) {

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	return doAttachUprobe(p, true, pid, absPath, offset)
}

func doAttachUprobe(prog *BPFProg, isUretprobe bool, pid int, path string, offset uint32) (*BPFLink, error) {
	retCBool := C.bool(isUretprobe)
	pidCint := C.int(pid)
	pathCString := C.CString(path)
	offsetCsizet := C.size_t(offset)

	link := C.bpf_program__attach_uprobe(prog.prog, retCBool, pidCint, pathCString, offsetCsizet)
	C.free(unsafe.Pointer(pathCString))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach u(ret)probe to program %s:%d with pid %s, ", path, offset, pid)
	}

	upType := Uprobe
	if isUretprobe {
		upType = Uretprobe
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      prog,
		linkType:  upType,
		eventName: fmt.Sprintf("%s:%d:%s", path, pid, offset),
	}
	return bpfLink, nil
}

func (p *BPFProg) AttachKprobeLegacy(kp string) (*BPFLink, error) {
	return doAttachKprobeLegacy(p, kp, false)
}

func (p *BPFProg) AttachKretprobeLegacy(kp string) (*BPFLink, error) {
	return doAttachKprobeLegacy(p, kp, true)
}

func doAttachKprobeLegacy(prog *BPFProg, kp string, isKretprobe bool) (*BPFLink, error) {
	cs := C.CString(kp)
	cbool := C.bool(isKretprobe)
	link := C.attach_kprobe_legacy(prog.prog, cs, cbool)
	C.free(unsafe.Pointer(cs))
	if C.IS_ERR_OR_NULL(unsafe.Pointer(link)) {
		return nil, errptrError(unsafe.Pointer(link), "failed to attach %s k(ret)probe using legacy debugfs API", kp)
	}

	kpType := KprobeLegacy
	if isKretprobe {
		kpType = KretprobeLegacy
	}

	bpfLink := &BPFLink{
		link:      link,
		prog:      prog,
		linkType:  kpType,
		eventName: kp,
	}
	prog.module.links = append(prog.module.links, bpfLink)
	return bpfLink, nil
}

var eventChannels = helpers.NewRWArray(maxEventChannels)

func (m *Module) InitRingBuf(mapName string, eventsChan chan []byte) (*RingBuffer, error) {
	bpfMap, err := m.GetMap(mapName)
	if err != nil {
		return nil, err
	}

	if eventsChan == nil {
		return nil, fmt.Errorf("events channel can not be nil")
	}

	slot := eventChannels.Put(eventsChan)
	if slot == -1 {
		return nil, fmt.Errorf("max ring buffers reached")
	}

	rb := C.init_ring_buf(bpfMap.fd, C.uintptr_t(slot))
	if rb == nil {
		return nil, fmt.Errorf("failed to initialize ring buffer")
	}

	ringBuf := &RingBuffer{
		rb:     rb,
		bpfMap: bpfMap,
		slot:   uint(slot),
	}
	m.ringBufs = append(m.ringBufs, ringBuf)
	return ringBuf, nil
}

func (rb *RingBuffer) Start() {
	rb.stop = make(chan struct{})
	rb.wg.Add(1)
	go rb.poll()
}

func (rb *RingBuffer) Stop() {
	if rb.stop != nil {
		// Tell the poll goroutine that it's time to exit
		close(rb.stop)

		// The event channel should be drained here since the consumer
		// may have stopped at this point. Failure to drain it will
		// result in a deadlock: the channel will fill up and the poll
		// goroutine will block in the callback.
		eventChan := eventChannels.Get(rb.slot).(chan []byte)
		go func() {
			for range eventChan {
			}
		}()

		// Wait for the poll goroutine to exit
		rb.wg.Wait()

		// Close the channel -- this is useful for the consumer but
		// also to terminate the drain goroutine above.
		close(eventChan)

		// This allows Stop() to be called multiple times safely
		rb.stop = nil
	}
}

func (rb *RingBuffer) Close() {
	if rb.closed {
		return
	}
	rb.Stop()
	C.ring_buffer__free(rb.rb)
	eventChannels.Remove(rb.slot)
	rb.closed = true
}

func (rb *RingBuffer) isStopped() bool {
	select {
	case <-rb.stop:
		return true
	default:
		return false
	}
}

func (rb *RingBuffer) poll() error {
	defer rb.wg.Done()

	for {
		err := C.ring_buffer__poll(rb.rb, 300)
		if rb.isStopped() {
			break
		}

		if err < 0 {
			if syscall.Errno(-err) == syscall.EINTR {
				continue
			}
			return fmt.Errorf("error polling ring buffer: %d", err)
		}
	}
	return nil
}

func (m *Module) InitPerfBuf(mapName string, eventsChan chan []byte, lostChan chan uint64, pageCnt int) (*PerfBuffer, error) {
	bpfMap, err := m.GetMap(mapName)
	if err != nil {
		return nil, fmt.Errorf("failed to init perf buffer: %v", err)
	}
	if eventsChan == nil {
		return nil, fmt.Errorf("failed to init perf buffer: events channel can not be nil")
	}

	perfBuf := &PerfBuffer{
		bpfMap:     bpfMap,
		eventsChan: eventsChan,
		lostChan:   lostChan,
	}

	slot := eventChannels.Put(perfBuf)
	if slot == -1 {
		return nil, fmt.Errorf("max number of ring/perf buffers reached")
	}

	pb := C.init_perf_buf(bpfMap.fd, C.int(pageCnt), C.uintptr_t(slot))
	if pb == nil {
		eventChannels.Remove(uint(slot))
		return nil, fmt.Errorf("failed to initialize perf buffer")
	}

	perfBuf.pb = pb
	perfBuf.slot = uint(slot)

	m.perfBufs = append(m.perfBufs, perfBuf)
	return perfBuf, nil
}

func (pb *PerfBuffer) Start() {
	pb.stop = make(chan struct{})
	pb.wg.Add(1)
	go pb.poll()
}

func (pb *PerfBuffer) Stop() {
	if pb.stop != nil {
		// Tell the poll goroutine that it's time to exit
		close(pb.stop)

		// The event and lost channels should be drained here since the consumer
		// may have stopped at this point. Failure to drain it will
		// result in a deadlock: the channel will fill up and the poll
		// goroutine will block in the callback.
		go func() {
			for range pb.eventsChan {
			}

			if pb.lostChan != nil {
				for range pb.lostChan {
				}
			}
		}()

		// Wait for the poll goroutine to exit
		pb.wg.Wait()

		// Close the channel -- this is useful for the consumer but
		// also to terminate the drain goroutine above.
		close(pb.eventsChan)
		close(pb.lostChan)

		// This allows Stop() to be called multiple times safely
		pb.stop = nil
	}
}

func (pb *PerfBuffer) Close() {
	if pb.closed {
		return
	}
	pb.Stop()
	C.perf_buffer__free(pb.pb)
	eventChannels.Remove(pb.slot)
	pb.closed = true
}

// todo: consider writing the perf polling in go as c to go calls (callback) are expensive
func (pb *PerfBuffer) poll() error {
	defer pb.wg.Done()

	for {
		select {
		case <-pb.stop:
			return nil
		default:
			err := C.perf_buffer__poll(pb.pb, 300)
			if err < 0 {
				if syscall.Errno(-err) == syscall.EINTR {
					continue
				}
				return fmt.Errorf("error polling perf buffer: %d", err)
			}
		}
	}
}

type TcAttachPoint uint32

const (
	BPFTcIngress       TcAttachPoint = C.BPF_TC_INGRESS
	BPFTcEgress        TcAttachPoint = C.BPF_TC_EGRESS
	BPFTcIngressEgress TcAttachPoint = C.BPF_TC_INGRESS | C.BPF_TC_EGRESS
	BPFTcCustom        TcAttachPoint = C.BPF_TC_CUSTOM
)

type TcFlags uint32

const (
	BpfTcFReplace TcFlags = C.BPF_TC_F_REPLACE
)

type TcHook struct {
	hook *C.struct_bpf_tc_hook
}

type TcOpts struct {
	ProgFd   int
	Flags    TcFlags
	ProgId   uint
	Handle   uint
	Priority uint
}

func tcOptsToC(tcOpts *TcOpts) *C.struct_bpf_tc_opts {
	if tcOpts == nil {
		return nil
	}
	opts := C.struct_bpf_tc_opts{}
	opts.sz = C.sizeof_struct_bpf_tc_opts
	opts.prog_fd = C.int(tcOpts.ProgFd)
	opts.flags = C.uint(tcOpts.Flags)
	opts.prog_id = C.uint(tcOpts.ProgId)
	opts.handle = C.uint(tcOpts.Handle)
	opts.priority = C.uint(tcOpts.Priority)

	return &opts
}

func tcOptsFromC(tcOpts *TcOpts, opts *C.struct_bpf_tc_opts) {
	if opts == nil {
		return
	}
	tcOpts.ProgFd = int(opts.prog_fd)
	tcOpts.Flags = TcFlags(opts.flags)
	tcOpts.ProgId = uint(opts.prog_id)
	tcOpts.Handle = uint(opts.handle)
	tcOpts.Priority = uint(opts.priority)
}

func (m *Module) TcHookInit() *TcHook {
	hook := C.struct_bpf_tc_hook{}
	hook.sz = C.sizeof_struct_bpf_tc_hook

	return &TcHook{
		hook: &hook,
	}
}

func (hook *TcHook) SetInterfaceByIndex(ifaceIdx int) {
	hook.hook.ifindex = C.int(ifaceIdx)
}

func (hook *TcHook) SetInterfaceByName(ifaceName string) error {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return err
	}
	hook.hook.ifindex = C.int(iface.Index)

	return nil
}

func (hook *TcHook) GetInterfaceIndex() int {
	return int(hook.hook.ifindex)
}

func (hook *TcHook) SetAttachPoint(attachPoint TcAttachPoint) {
	hook.hook.attach_point = uint32(attachPoint)
}

func (hook *TcHook) SetParent(a int, b int) {
	parent := (((a) << 16) & 0xFFFF0000) | ((b) & 0x0000FFFF)
	hook.hook.parent = C.uint(parent)
}

func (hook *TcHook) Create() error {
	ret := C.bpf_tc_hook_create(hook.hook)
	if ret < 0 {
		return syscall.Errno(-ret)
	}

	return nil
}

func (hook *TcHook) Destroy() error {
	ret := C.bpf_tc_hook_destroy(hook.hook)
	if ret < 0 {
		return syscall.Errno(-ret)
	}

	return nil
}

func (hook *TcHook) Attach(tcOpts *TcOpts) error {
	opts := tcOptsToC(tcOpts)
	ret := C.bpf_tc_attach(hook.hook, opts)
	if ret < 0 {
		return syscall.Errno(-ret)
	}
	tcOptsFromC(tcOpts, opts)

	return nil
}

func (hook *TcHook) Detach(tcOpts *TcOpts) error {
	opts := tcOptsToC(tcOpts)
	ret := C.bpf_tc_detach(hook.hook, opts)
	if ret < 0 {
		return syscall.Errno(-ret)
	}
	tcOptsFromC(tcOpts, opts)

	return nil
}

func (hook *TcHook) Query(tcOpts *TcOpts) error {
	opts := tcOptsToC(tcOpts)
	ret := C.bpf_tc_query(hook.hook, opts)
	if ret < 0 {
		return syscall.Errno(-ret)
	}
	tcOptsFromC(tcOpts, opts)

	return nil
}
