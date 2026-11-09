package auth

import (
	"errors"
	"net/http"
)

var ErrUnauthorized = errors.New("request unauthorized")

type Auth interface {
	Handle(w http.ResponseWriter, r *http.Request) (user string, err error)
}

// Router serves the latere login, callback, and logout routes.
// Route reports whether the request was one of those.
type Router interface {
	Route(w http.ResponseWriter, r *http.Request) bool
}
