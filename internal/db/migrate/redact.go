package migrate

import "net/url"

// Redact removes the password from a store URI before it reaches a log
// line or an error message.
func Redact(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "invalid store uri"
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}
