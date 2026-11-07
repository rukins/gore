package migrate

import (
	"sync"
	"time"
)

// NextID is the primary key for a link or a visit.
//
// It is Unix microseconds, and a process-local counter bumps it when the
// clock does not move, so one process never issues the same id twice.
// Two processes can still pick the same microsecond. The insert retries
// with the next value. That collision is not an alias conflict.
var (
	idMu   sync.Mutex
	lastID int64
)

func NextID() int64 {
	idMu.Lock()
	defer idMu.Unlock()
	n := time.Now().UTC().UnixMicro()
	if n <= lastID {
		n = lastID + 1
	}
	lastID = n
	return n
}
