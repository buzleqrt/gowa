package command

import "sync/atomic"

var locked atomic.Bool

func SetLocked(value bool) {
	locked.Store(value)
}

func IsLocked() bool {
	return locked.Load()
}
