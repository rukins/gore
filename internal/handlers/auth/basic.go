package auth

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/utils"
)

type basicAuth struct{}

func NewBasicAuth() Auth {
	return &basicAuth{}
}

func (a *basicAuth) Handle(w http.ResponseWriter, r *http.Request) (user string, err error) {
	w.Header().Set("WWW-Authenticate", `Basic realm="gore"`)

	u, p, ok := r.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		err = fmt.Errorf("%w: failed to parsing basic auth", ErrUnauthorized)
		return
	}

	// check if the IP failure attempts are too much
	// if so, direct abort the request without checking credentials
	ip := utils.ReadIP(r)
	if i, ok := blocklist.Load(ip); ok {
		info := i.(*blockinfo)
		count := atomic.LoadInt64(&info.failCount)
		if count > maxFailureAttempts {
			// if the ip is under block, then directly abort
			last := info.lastFail.Load().(time.Time)
			bloc := info.blockTime.Load().(time.Duration)

			if time.Now().UTC().Sub(last.Add(bloc)) < 0 {
				log.Printf("block ip %v, too much failure attempts. Block time: %v, release until: %v\n",
					ip, bloc, last.Add(bloc))
				// Every other refusal here sets a status; without one
				// this would be answered 200.
				w.WriteHeader(http.StatusTooManyRequests)
				err = fmt.Errorf("%w: too much failure attempts", ErrUnauthorized)
				return
			}

			// clear the failcount, but increase the next block time
			atomic.StoreInt64(&info.failCount, 0)
			info.blockTime.Store(bloc * 2)
		}
	}

	defer func() {
		if !errors.Is(err, ErrUnauthorized) {
			return
		}

		if i, ok := blocklist.Load(ip); !ok {
			info := &blockinfo{
				failCount: 1,
			}
			info.lastFail.Store(time.Now().UTC())
			info.blockTime.Store(time.Second * 10)

			blocklist.Store(ip, info)
		} else {
			info := i.(*blockinfo)
			atomic.AddInt64(&info.failCount, 1)
			info.lastFail.Store(time.Now().UTC())
		}
	}()

	found := false
	for _, account := range config.Conf.Auth.Basic {
		if u == account.Username && p == account.Password {
			found = true
			break
		}
	}
	if !found {
		w.WriteHeader(http.StatusUnauthorized)
		return "", fmt.Errorf("%w: username or password is invalid", ErrUnauthorized)
	}
	return u, nil
}
