package proc

type goroutineCache struct {
	partialGCache map[int64]*G
	allGCache     []*G

	allgentryAddr, allglenAddr uint64
}

func (gcache *goroutineCache) init(bi *BinaryInfo) {
	var err error

	goimage := bi.goImage()
	rdr := goimage.DwarfReader()
	if rdr == nil {
		return
	}

	gcache.allglenAddr, err = rdr.AddrFor("runtime.allglen", goimage.StaticBase, bi.Arch.PtrSize())
	if err != nil {
		bi.logger.Debugf("could not find runtime.allglen in %s: %v", goimage.Path, err)
	}

	rdr.Seek(0)
	gcache.allgentryAddr, err = rdr.AddrFor("runtime.allgs", goimage.StaticBase, bi.Arch.PtrSize())
	if err != nil {
		// try old name (pre Go 1.6)
		rdr.Seek(0)
		gcache.allgentryAddr, err = rdr.AddrFor("runtime.allg", goimage.StaticBase, bi.Arch.PtrSize())
		if err != nil {
			bi.logger.Debugf("could not find runtime.allgs in %s: %v", goimage.Path, err)
		}
	}
}

func (gcache *goroutineCache) getRuntimeAllg(bi *BinaryInfo, mem MemoryReadWriter) (uint64, uint64, error) {
	if (gcache.allglenAddr == 0 || gcache.allgentryAddr == 0) && bi.goImage().IsGo {
		// The Go runtime could be in a shared library loaded after the cache was
		// initialized (e.g. a Go library loaded with dlopen by a non-Go program).
		gcache.init(bi)
	}
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
