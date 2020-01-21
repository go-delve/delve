package proc

import "encoding/binary"

type goroutineCache struct {
	partialGCache map[int]*G
	allGCache     []*G

	allgentryAddr, allglenAddr uint64
}

func (gcache *goroutineCache) init(bi *BinaryInfo) {
	var err error

	exeimage := bi.Images[0]
	rdr := exeimage.DwarfReader()

	gcache.allglenAddr, _ = rdr.AddrFor("runtime.allglen", exeimage.StaticBase)

	rdr.Seek(0)
	gcache.allgentryAddr, err = rdr.AddrFor("runtime.allgs", exeimage.StaticBase)
	if err != nil {
		// try old name (pre Go 1.6)
		gcache.allgentryAddr, _ = rdr.AddrFor("runtime.allg", exeimage.StaticBase)
	}
}

func (gcache *goroutineCache) getRuntimeAllg(bi *BinaryInfo, mem MemoryReadWriter) (uint64, uint64, error) {
	if gcache.allglenAddr == 0 || gcache.allgentryAddr == 0 {
		return 0, 0, ErrNoRuntimeAllG
	}
	allglenBytes := make([]byte, 8)
	_, err := mem.ReadMemory(allglenBytes, uintptr(gcache.allglenAddr))
	if err != nil {
		return 0, 0, err
	}
	allglen := binary.LittleEndian.Uint64(allglenBytes)

	faddr := make([]byte, bi.Arch.PtrSize())
	_, err = mem.ReadMemory(faddr, uintptr(gcache.allgentryAddr))
	if err != nil {
		return 0, 0, err
	}
	allgptr := binary.LittleEndian.Uint64(faddr)

	return allgptr, allglen, nil
}

func (gcache *goroutineCache) addGoroutine(g *G) {
	if gcache.partialGCache == nil {
		gcache.partialGCache = make(map[int]*G)
	}
	gcache.partialGCache[g.ID] = g
}

// Clear clears the cached contents of the cache for runtime.allgs.
func (gcache *goroutineCache) Clear() {
	gcache.partialGCache = nil
	gcache.allGCache = nil
}
