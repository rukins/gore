package auth

import (
	"sync"
	"sync/atomic"
	"time"
)

const maxFailureAttempts = 3

// blocklist holds the ip that should be blocked for further requests.
//
// This map may keep grow without releasing memory because of
// continuously attempts. we also do not persist this type of block info
// to the disk, which means if we reboot the service then all the blocker
// are gone and they can attack the server again.
// We clear the map very month.
var blocklist sync.Map // map[string]*blockinfo{}

type blockinfo struct {
	failCount int64
	lastFail  atomic.Value // time.Time
	blockTime atomic.Value // time.Duration
}

func init() {
	t := time.NewTicker(time.Hour * 24 * 30)
	go func() {
		for range t.C {
			blocklist.Range(func(k, v any) bool {
				blocklist.Delete(k)
				return true
			})
		}
	}()
}
