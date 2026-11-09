package handlers

import (
	"fmt"
	"net/http"

	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/handlers/auth"
)

func (s *service) handleAuth(w http.ResponseWriter, r *http.Request) (user string, err error) {
	switch config.Conf.Auth.Enable {
	case config.None:
		return
	case config.Latere:
		// NewLatereAuth returns a nil interface when the deployment has
		// no principals or no client. Calling Handle on that panics.
		// There is no login route to send them to, and basic auth would
		// be the fallback an unknown mode already refuses.
		if s.latere == nil {
			w.WriteHeader(http.StatusForbidden)
			return "", fmt.Errorf("%w: latere auth is not configured",
				auth.ErrUnauthorized)
		}
		return s.latere.Handle(w, r)
	case config.Basic:
		// Handled below.
	default:
		// An unknown mode used to fall through to basic auth. Refuse
		// administration instead of guessing which login was meant.
		w.WriteHeader(http.StatusForbidden)
		return "", fmt.Errorf("%w: unknown auth mode %q",
			auth.ErrUnauthorized, config.Conf.Auth.Enable)
	}

	return s.basic.Handle(w, r)
}
