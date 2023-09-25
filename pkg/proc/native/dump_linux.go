package native

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/proc"
)

func (p *nativeProcess) MemoryMap() ([]proc.MemoryMapEntry, error) {
	const VmFlagsPrefix = "VmFlags:"

	smapsbuf, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps", p.pid))
	if err != nil {
		// Older versions of Linux don't have smaps but have maps which is in a similar format.
		smapsbuf, err = os.ReadFile(fmt.Sprintf("/proc/%d/maps", p.pid))
		if err != nil {
			return nil, err
		}
	}
	smapsLines := strings.Split(string(smapsbuf), "\n")
	r := make([]proc.MemoryMapEntry, 0)

smapsLinesLoop:
	for i := 0; i < len(smapsLines); {
		line := smapsLines[i]
		if line == "" {
			i++
			continue
		}
		start, end, perm, offset, dev, filename, err := parseSmapsHeaderLine(i+1, line)
		if err != nil {
			return nil, err
		}
		var vmflags []string
		for i++; i < len(smapsLines); i++ {
			line := smapsLines[i]
			if line == "" || line[0] < 'A' || line[0] > 'Z' {
				break
			}
			if strings.HasPrefix(line, VmFlagsPrefix) {
				vmflags = strings.Split(strings.TrimSpace(line[len(VmFlagsPrefix):]), " ")
			}
		}

		for i := range vmflags {
			switch vmflags[i] {
			case "pf":
				// pure PFN range, see https://github.com/go-delve/delve/issues/2630
				continue smapsLinesLoop
			case "dd":
				// "don't dump"
				continue smapsLinesLoop
			case "io":
				continue smapsLinesLoop
			}
		}
		if strings.HasPrefix(dev, "00:") {
			filename = ""
			offset = 0
		}

		r = append(r, proc.MemoryMapEntry{
			Addr: start,
			Size: end - start,

			Read:  perm[0] == 'r',
			Write: perm[1] == 'w',
			Exec:  perm[2] == 'x',

			Filename: filename,
			Offset:   offset,
		})

	}
	return r, nil
}

func parseSmapsHeaderLine(lineno int, in string) (start, end uint64, perm string, offset uint64, dev, filename string, err error) {
	fields := strings.SplitN(in, " ", 6)
	if len(fields) != 6 {
		err = fmt.Errorf("malformed /proc/pid/maps on line %d: %q (wrong number of fields)", lineno, in)
		return
	}

	v := strings.Split(fields[0], "-")
	if len(v) != 2 {
		err = fmt.Errorf("malformed /proc/pid/maps on line %d: %q (bad first field)", lineno, in)
		return
	}
	start, err = strconv.ParseUint(v[0], 16, 64)
	if err != nil {
		err = fmt.Errorf("malformed /proc/pid/maps on line %d: %q (%v)", lineno, in, err)
		return
	}
	end, err = strconv.ParseUint(v[1], 16, 64)
	if err != nil {
		err = fmt.Errorf("malformed /proc/pid/maps on line %d: %q (%v)", lineno, in, err)
		return
	}

	perm = fields[1]
	if len(perm) < 4 {
		err = fmt.Errorf("malformed /proc/pid/maps on line %d: %q (permissions column too short)", lineno, in)
		return
	}

	offset, err = strconv.ParseUint(fields[2], 16, 64)
	if err != nil {
		err = fmt.Errorf("malformed /proc/pid/maps on line %d: %q (%v)", lineno, in, err)
		return
	}

	dev = fields[3]

	// fields[4] -> inode

	filename = strings.TrimLeft(fields[5], " ")
	return

}
