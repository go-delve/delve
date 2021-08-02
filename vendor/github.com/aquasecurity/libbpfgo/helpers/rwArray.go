package helpers

import (
	"sync"
)

type slot struct {
	value interface{}
	used  bool
}

// RWArray allows for multiple concurrent readers but
// only a single writer. The writers lock a mutex while the readers
// are lock free.
// It is implemented as an array of slots where each slot holds a
// value (of type interface{}) and a boolean marker to indicate if it's
// in use or not. The insertion (Put) performs a linear probe
// looking for an available slot as indicated by the in-use marker.
// While probing, it is not touching the value itself, as it's
// being read without a lock by the readers.
type RWArray struct {
	slots []slot
	mux   sync.Mutex
}

func NewRWArray(capacity uint) RWArray {
	return RWArray{
		slots: make([]slot, capacity),
	}
}

func (a *RWArray) Put(v interface{}) int {
	a.mux.Lock()
	defer a.mux.Unlock()

	limit := len(a.slots)

	for i := 0; i < limit; i++ {
		if !a.slots[i].used {
			a.slots[i].value = v
			a.slots[i].used = true
			return i
		}
	}

	return -1
}

func (a *RWArray) Remove(index uint) {
	a.mux.Lock()
	defer a.mux.Unlock()

	if int(index) >= len(a.slots) {
		return
	}

	a.slots[index].value = nil
	a.slots[index].used = false
}

func (a *RWArray) Get(index uint) interface{} {
	if int(index) >= len(a.slots) {
		return nil
	}

	// N.B. If slot[index].used == false, this is technically
	// a race since Put() might be putting the value in there
	// at the same time.
	return a.slots[index].value
}

func (a *RWArray) Capacity() uint {
	return uint(len(a.slots))
}
