package command

import "sync/atomic"

var locked atomic.Bool

// SetLocked mengunci atau membuka bot. Saat terkunci hanya perintah lock
// yang tetap diproses.
func SetLocked(value bool) {
	locked.Store(value)
}

func IsLocked() bool {
	return locked.Load()
}
