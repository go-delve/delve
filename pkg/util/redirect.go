package util

import (
	"os"
	"sync"
)

var (
	redirectMap = &RedirectStroe{rs: sync.Map{}}
)

type RedirectStroe struct {
	rs sync.Map
}

func GetRedirectStrore() *RedirectStroe {
	return redirectMap
}

func (r *RedirectStroe) Store(key string, val *Pipe) {
	r.rs.Store(key, val)
}

func (r *RedirectStroe) Load(key string) (*Pipe, bool) {
	val, ok := r.rs.Load(key)
	if ok {
		return val.(*Pipe), true
	}

	return nil, false
}

type Pipe struct {
	Reader *os.File
	Writer *os.File
}
