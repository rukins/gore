package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/handlers/auth"
)

// recordingRouter is the latere login flow as the console sees it. The
// real PKCE redirect is tested against Route itself.
type recordingRouter struct {
	hit string
}

func (recordingRouter) Handle(http.ResponseWriter, *http.Request) (string, error) {
	return "", nil
}

func (r *recordingRouter) Route(w http.ResponseWriter, req *http.Request) bool {
	p := config.Conf.AdminPrefix()
	switch {
	case strings.HasPrefix(req.URL.Path, p+auth.LoginPath),
		strings.HasPrefix(req.URL.Path, p+auth.CallbackPath),
		strings.HasPrefix(req.URL.Path, p+auth.LogoutPath):
		r.hit = req.URL.Path
		w.WriteHeader(http.StatusNoContent)
		return true
	default:
		return false
	}
}

// TestAdminDispatchesAuthRoutes checks the login flow is answered on the
// console prefix, and that the same paths on the shortener are not a page.
func TestAdminDispatchesAuthRoutes(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.S.Prefix = "/s/"
	config.Conf.A.Prefix = "/a/"

	routes := &recordingRouter{}
	s := &service{latere: routes}
	for _, path := range []string{
		"/a/" + auth.LoginPath,
		"/a/" + auth.CallbackPath,
		"/a/" + auth.LogoutPath,
	} {
		routes.hit = ""
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		s.AHandler().ServeHTTP(w, r)
		if w.Code != http.StatusNoContent || routes.hit != path {
			t.Fatalf("%s: status %d, handled %q", path, w.Code, routes.hit)
		}
	}

	// The public prefix does not grow a login route of its own.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/s/"+auth.LoginPath, nil)
	if err := (&service{}).serveStatic(r.Context(), w, r); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("shortener login route status = %d, want %d", w.Code, http.StatusNotFound)
	}

	// Without a relying party the console path is not a page either.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/a/"+auth.LoginPath, nil)
	(&service{}).AHandler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unconfigured login route status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestHandleAuthSendsAnonymousToLogin covers the path a visitor takes when
// they open the admin dashboard without a session.
func TestHandleAuthSendsAnonymousToLogin(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Auth.Enable = config.Latere
	config.Conf.Host = "https://example.com"
	config.Conf.S.Prefix = "/s/"
	config.Conf.A.Prefix = "/a/"
	t.Setenv("AUTH_ALLOWED_PRINCIPALS", "hi@example.com")
	t.Setenv("AUTH_CLIENT_ID", "gore-test")
	t.Setenv("AUTH_COOKIE_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("AUTH_URL", "https://auth.example.test")
	t.Setenv("AUTH_REDIRECT_URL", "")

	latere := auth.NewLatereAuth("/a/")
	if latere == nil {
		t.Fatal("expected a configured relying party")
	}
	s := &service{latere: latere}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/a/", nil)

	user, err := s.handleAuth(w, r)
	if err == nil {
		t.Fatal("handleAuth accepted a request with no session")
	}
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("handleAuth err = %v, want unauthorized", err)
	}
	if user != "" {
		t.Errorf("handleAuth named %q for an anonymous request", user)
	}
	if w.Code != http.StatusFound {
		t.Fatalf("handleAuth returned %v, want a %v to the login route", w.Code, http.StatusFound)
	}

	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/a/"+auth.LoginPath) {
		t.Errorf("handleAuth redirected to %q, want the login route", loc)
	}
	u, err := http.NewRequest(http.MethodGet, loc, nil)
	if err != nil {
		t.Fatalf("cannot parse %q: %v", loc, err)
	}
	if got := u.URL.Query().Get("return_to"); got != "/a/" {
		t.Errorf("return_to = %q, want the originally requested page", got)
	}
}

// TestHandleAuthUnconfiguredLatereRefuses checks a latere deployment that
// failed to build its client. Administration must fail closed, not panic.
func TestHandleAuthUnconfiguredLatereRefuses(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Auth.Enable = config.Latere

	s := &service{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/a/", nil)

	user, err := s.handleAuth(w, r)
	if err == nil || !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("handleAuth err = %v, want unauthorized", err)
	}
	if user != "" {
		t.Errorf("handleAuth named %q", user)
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("handleAuth returned %v, want %v", w.Code, http.StatusForbidden)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("unconfigured latere offered basic auth: WWW-Authenticate=%q", got)
	}
}

// TestHandleAuthUnknownModeRefuses covers a stale or mistyped auth.enable.
// Administration must fail closed rather than quietly fall through to a
// different login. Public redirects do not go through handleAuth and keep
// working.
func TestHandleAuthUnknownModeRefuses(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Auth.Enable = "no-such-mode"

	s := &service{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/a/", nil)

	user, err := s.handleAuth(w, r)
	if err == nil {
		t.Fatal("handleAuth accepted a request under an unknown auth mode")
	}
	if user != "" {
		t.Errorf("handleAuth named %q under an unknown auth mode", user)
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("handleAuth returned %v, want %v", w.Code, http.StatusForbidden)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("handleAuth fell through to basic auth: WWW-Authenticate=%q", got)
	}
}

// TestUnauthorizedChangeKeepsItsOwnAnswer checks that a change from someone
// not signed in is answered by handleAuth, not turned into a 400.
func TestUnauthorizedChangeKeepsItsOwnAnswer(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Auth.Enable = "no-such-mode"

	s := &service{}
	w := httptest.NewRecorder()
	s.sHandlerPost(w, httptest.NewRequest(http.MethodPost, "/s/", strings.NewReader(`{"op":"create","alias":"x","data":{}}`)))
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want the %d handleAuth sets", w.Code, http.StatusForbidden)
	}
}
