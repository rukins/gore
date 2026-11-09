package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rukins/gore/internal/config"
)

func TestPublicIndexDoesNotServeTheConsole(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.S.Prefix = "/s/"
	config.Conf.A.Prefix = "/a/"

	s := &service{}
	for _, path := range []string{
		"/s/?mode=admin",
		"/s/?mode=index-pro",
		"/s/?mode=overview",
		"/s/?mode=stats",
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if err := s.sIndex(r.Context(), w, r); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "" {
			t.Fatalf("%s redirected to %s", path, loc)
		}
	}

	w := httptest.NewRecorder()
	s.SHandler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/s/", strings.NewReader(`{}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /s status = %d, want 404", w.Code)
	}
}

func TestConsoleRendersOnItsPrefix(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Auth.Enable = config.None
	config.Conf.Host = "https://example.com"
	config.Conf.S.Prefix = "/s/"
	config.Conf.A.Prefix = "/a/"

	s := &service{dashboard: template.Must(template.New("dashboard").Parse(
		"admin={{.AdminView}} short={{.ShortPrefix}}",
	))}
	w := httptest.NewRecorder()
	s.AHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/a/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, "admin=true") || !strings.Contains(body, "short=/s/") {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "/a") {
		t.Fatalf("the console page names its own prefix: %s", body)
	}
}

func TestConsoleServesItsAssets(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.A.Prefix = "/a/"

	s := &service{statics: fstest.MapFS{
		"app.js": &fstest.MapFile{Data: []byte("ok")},
	}}
	w := httptest.NewRecorder()
	s.AHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/a/.static/app.js", nil))
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Content-Type"); got != "text/javascript" {
		t.Fatalf("content-type = %q", got)
	}
}

func TestConsolePostIsTheOperatorAPI(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Auth.Enable = "no-such-mode"
	config.Conf.A.Prefix = "/a/"

	s := &service{}
	w := httptest.NewRecorder()
	s.AHandler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/a/", strings.NewReader(`{"op":"create"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want the refusal handleAuth sets", w.Code)
	}
}
