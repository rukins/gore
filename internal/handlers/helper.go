package handlers

import (
	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/handlers/auth"
)

// logoutURL is the route that ends a latere session, or the empty string
// when the deployment has no session to end.
func logoutURL() string {
	if config.Conf.Auth.Enable != config.Latere {
		return ""
	}
	return config.Conf.S.Prefix + auth.LogoutPath
}
