package proc

type goroutineCache struct {
	partialGCache map[int64]*G
	allGCache     []*G

	allgentryAddr, allglenAddr uint64
}

func (gcache *goroutineCache) init(bi *BinaryInfo) {
	var err error

	exeimage := bi.Images[0]
	rdr := exeimage.DwarfReader()

	gcache.allglenAddr, _ = rdr.AddrFor("runtime.allglen", exeimage.StaticBase, bi.Arch.PtrSize())

	rdr.Seek(0)
	gcache.allgentryAddr, err = rdr.AddrFor("runtime.allgs", exeimage.StaticBase, bi.Arch.PtrSize())
	if err != nil {
		// try old name (pre Go 1.6)
		gcache.allgentryAddr, _ = rdr.AddrFor("runtime.allg", exeimage.StaticBase, bi.Arch.PtrSize())
	}
}

func (gcache *goroutineCache) getRuntimeAllg(bi *BinaryInfo, mem MemoryReadWriter) (uint64, uint64, error) {
	if gcache.allglenAddr == 0 || gcache.allgentryAddr == 0 {
		return 0, 0, ErrNoRuntimeAllG
	}
	allglen, err := readUintRaw(mem, gcache.allglenAddr, int64(bi.Arch.PtrSize()))
	if err != nil {
		return 0, 0, err
	}

	allgptr, err := readUintRaw(mem, gcache.allgentryAddr, int64(bi.Arch.PtrSize()))
	if err != nil {
		return 0, 0, err
	}
	return allgptr, allglen, nil
}

func (gcache *goroutineCache) addGoroutine(g *G) {
	if gcache.partialGCache == nil {
		gcache.partialGCache = make(map[int64]*G)
	}
	gcache.partialGCache[g.ID] = g
}

// Clear clears the cached contents of the cache for runtime.allgs.
func (gcache *goroutineCache) Clear() {
	gcache.partialGCache = nil
	gcache.allGCache = nil
}
