package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rukins/gore/internal/config"
)

// TestXHandlerUsesTheSiteRepository checks the go-import meta tag names
// the repository of the site that was asked for. Getting this wrong sends
// `go get` to another organisation, and an empty document makes it fail
// with no meta tag at all.
func TestXHandlerUsesTheSiteRepository(t *testing.T) {
	page, err := prepareImportPage(os.DirFS("../.."))
	if err != nil {
		t.Fatalf("prepare import page: %v", err)
	}

	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Host = "https://example.com"
	config.Conf.X.Enable = true
	config.Conf.X.Prefix = "/x/"
	config.Conf.X.VCS = "git"
	config.Conf.X.RepoPath = "https://github.com/rukins"
	config.Conf.X.GoDocHost = "https://pkg.go.dev/"
	config.Conf.Hosts = map[string]config.SiteOverride{
		"examplepkg.com": {RepoPath: "https://github.com/examplepkg"},
	}

	s := &service{x: page}
	h := s.XHandler()

	for _, tt := range []struct{ host, want string }{
		{"example.com", "example.com/x/gore git https://github.com/rukins/gore"},
		{"examplepkg.com", "examplepkg.com/x/lockfree git https://github.com/examplepkg/lockfree"},
	} {
		t.Run(tt.host, func(t *testing.T) {
			alias := "gore"
			if tt.host == "examplepkg.com" {
				alias = "lockfree"
			}
			r := httptest.NewRequest(http.MethodGet, "/x/"+alias, nil)
			r.Host = tt.host
			w := httptest.NewRecorder()
			h(w, r)

			body := w.Body.String()
			if !strings.Contains(body, tt.want) {
				t.Fatalf("go-import does not name the site repository.\n want %v\n body %v",
					tt.want, body)
			}
			if strings.Contains(body, "Short links on") {
				t.Fatalf("go-import document was wrapped in the page layout:\n%s", body)
			}
		})
	}
}
