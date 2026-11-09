package auth

import (
	"cmp"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/rukins/gore/internal/config"
	"latere.ai/x/pkg/oidc"
)

// Routes for the login flow. They sit under the console prefix beside its
// static files. The shortener prefix is public, and a login route there
// would publish the way in. An alias may not begin with a dot, so the
// same names under the shortener still cannot collide with a short link.
const (
	LoginPath    = ".auth/login"
	CallbackPath = ".auth/callback"
	LogoutPath   = ".auth/logout"
)

// latereAuth is the relying party for auth.latere.ai. It runs the
// authorization code + PKCE flow in the browser and keeps the result in an
// encrypted session cookie, so handleAuth can name the visitor.
//
// There is one client per site. A relying party's callback URL is fixed
// when the client is built, and it has to be on the site the login
// started from: sending a visitor who signed in at one site back to
// another leaves them looking at the wrong domain, with the session
// cookie set on that domain instead of theirs. The session cookie carries
// the "__Host-" prefix, so it is scoped to one origin and each site is
// signed into separately, which is what it should be.
type latereAuth struct {
	clients map[string]*oidc.Client // by hostname
	allowed map[string]bool         // lowercased email or principal id (sub)
}

// newLatereAuth builds the relying party from the environment. It returns
// nil when the deployment is not configured for it, and every request then
// fails closed rather than falling back to an anonymous session.
func NewLatereAuth(prefix string) Auth {
	allowed := principalSet(os.Getenv("AUTH_ALLOWED_PRINCIPALS"))
	if len(allowed) == 0 {
		log.Println("AUTH_ALLOWED_PRINCIPALS is unset, latere logins will be rejected")
		return nil
	}

	clients := map[string]*oidc.Client{}
	for host, redirect := range callbackURLs(prefix) {
		cfg := oidc.LoadConfig()
		cfg.RedirectURL = redirect

		c := oidc.New(cfg)
		if c == nil {
			// oidc.New logs the specific reason: no AUTH_CLIENT_ID, or a
			// public client without AUTH_COOKIE_KEY to encrypt the
			// session with.
			log.Println("latere auth is not configured, logins will be rejected")
			return nil
		}
		clients[host] = c
		log.Printf("latere auth enabled for %v, callback %v", host, redirect)
	}
	if len(clients) == 0 {
		return nil
	}

	log.Printf("latere auth enabled: %v principals, %v sites",
		len(allowed), len(clients))
	return &latereAuth{clients: clients, allowed: allowed}
}

func (a *latereAuth) Handle(w http.ResponseWriter, r *http.Request) (user string, err error) {
	if user = a.user(w, r); user != "" {
		return user, nil
	}
	// These GETs are served by Route. Handle only answers an admin request
	// that has no session, by sending the visitor to the login route.
	http.Redirect(w, r, a.loginURL(config.Conf.AdminPrefix(), r), http.StatusFound)
	return "", fmt.Errorf("%w: no signed in latere principal", ErrUnauthorized)
}

// Route serves login, callback, and logout. The console calls it for
// every dotted path under its prefix; other paths are left for its
// static files. A nil receiver or a missing client handles nothing,
// rather than panicking on the method call.
func (a *latereAuth) Route(w http.ResponseWriter, r *http.Request) bool {
	if a == nil {
		return false
	}
	c := a.clientFor(r)
	if c == nil {
		return false
	}
	prefix := config.Conf.AdminPrefix()
	switch {
	case strings.HasPrefix(r.URL.Path, prefix+LoginPath):
		c.HandleLogin(w, r)
	case strings.HasPrefix(r.URL.Path, prefix+CallbackPath):
		c.HandleCallback(w, r)
	case strings.HasPrefix(r.URL.Path, prefix+LogoutPath):
		c.HandleLogout(w, r)
	default:
		return false
	}
	return true
}

// callbackURLs is the callback each site is reached on, keyed by hostname.
//
// Every site needs its own, and each has to be registered with the
// authorization server. AUTH_REDIRECT_URL still overrides the primary
// site's, for a deployment sitting behind a different public name; the
// others are derived, since only one value can be given that way.
func callbackURLs(prefix string) map[string]string {
	primary := config.Conf.Hostname()
	scheme := "https"
	if u, err := url.Parse(config.Conf.Host); err == nil && u.Scheme != "" {
		scheme = u.Scheme
	}

	out := map[string]string{
		primary: cmp.Or(os.Getenv("AUTH_REDIRECT_URL"),
			strings.TrimRight(config.Conf.Host, "/")+prefix+CallbackPath),
	}
	for name := range config.Conf.Hosts {
		host := config.NormalizeHost(name)
		if host == "" || host == primary {
			continue
		}
		out[host] = scheme + "://" + host + prefix + CallbackPath
	}
	return out
}

// clientFor returns the relying party for the site a request arrived on.
// An unrecognised Host header resolves to the primary site, as everything
// else host-derived does.
func (a *latereAuth) clientFor(r *http.Request) *oidc.Client {
	if a == nil {
		return nil
	}
	if c, ok := a.clients[config.Conf.SiteFor(r.Host).Host]; ok {
		return c
	}
	return a.clients[config.Conf.Hostname()]
}

// principalSet parses a comma separated list of emails or principal ids.
func principalSet(s string) map[string]bool {
	out := map[string]bool{}
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out[p] = true
		}
	}
	return out
}

// user returns the signed in principal, or the empty string. A token from
// auth.latere.ai proves who is calling, not that they may administer this
// deployment, so the allowlist decides separately.
func (a *latereAuth) user(w http.ResponseWriter, r *http.Request) string {
	if a == nil {
		return ""
	}
	c := a.clientFor(r)
	if c == nil {
		return ""
	}
	return a.permit(c.UserFromRequest(w, r))
}

// permit names the principal when the allowlist admits it, and returns the
// empty string otherwise. Split from user so the decision can be tested
// without an auth service.
func (a *latereAuth) permit(u *oidc.User) string {
	if a == nil || u == nil {
		return ""
	}
	if a.allowed[strings.ToLower(u.Email)] || a.allowed[strings.ToLower(u.Sub)] {
		return cmp.Or(u.Email, u.Sub)
	}
	log.Printf("latere principal not allowed: sub=%v email=%v", u.Sub, u.Email)
	return ""
}

// loginURL sends the visitor to the login route, remembering where they
// were so the callback can put them back.
func (a *latereAuth) loginURL(prefix string, r *http.Request) string {
	q := url.Values{}
	q.Set("return_to", r.URL.RequestURI())
	return prefix + LoginPath + "?" + q.Encode()
}
