package proc

type goroutineCache struct {
	partialGCache map[int64]*G
	allGCache     []*G

	allgentryAddr, allglenAddr uint64
}

// init looks up the addresses of runtime.allgs and runtime.allglen. The Go
// runtime is usually in the executable, but it can also be in a shared
// library (buildmode=c-shared) loaded by a non-Go program, so all images are
// searched.
func (gcache *goroutineCache) init(bi *BinaryInfo) {
	for _, image := range bi.Images {
		rdr := image.DwarfReader()
		if rdr == nil {
			continue
		}

		allglenAddr, err := rdr.AddrFor("runtime.allglen", image.StaticBase, bi.Arch.PtrSize())
		if err != nil {
			continue
		}

		rdr.Seek(0)
		allgentryAddr, err := rdr.AddrFor("runtime.allgs", image.StaticBase, bi.Arch.PtrSize())
		if err != nil {
			// try old name (pre Go 1.6)
			rdr.Seek(0)
			allgentryAddr, err = rdr.AddrFor("runtime.allg", image.StaticBase, bi.Arch.PtrSize())
			if err != nil {
				continue
			}
		}

		gcache.allglenAddr, gcache.allgentryAddr = allglenAddr, allgentryAddr
		return
	}
}

func (gcache *goroutineCache) getRuntimeAllg(bi *BinaryInfo, mem MemoryReadWriter) (uint64, uint64, error) {
	if gcache.allglenAddr == 0 || gcache.allgentryAddr == 0 {
		// The image containing the Go runtime may have been loaded after the
		// cache was initialized (e.g. a Go shared library loaded with dlopen).
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
