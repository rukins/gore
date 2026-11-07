package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/db"
	"github.com/rukins/gore/internal/models"
)

// recordingStore captures the links checkvcs creates.
type recordingStore struct {
	db.Store
	mu    sync.Mutex
	saved []*models.Link
}

func (s *recordingStore) StoreAlias(_ context.Context, r *models.Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *r
	s.saved = append(s.saved, &c)
	return nil
}

// TestCheckVCSProbesTheSiteOrganisation is the sharpest edge in serving
// two sites from one process.
//
// checkvcs runs when /s/<alias> finds no link. It requests
// repo_path + "/" + alias and, on 200, creates a link row. If the repo
// path came from the process rather than from the site the request
// arrived on, a miss on examplepkg.com would probe the primary site's organisation
// and could mint a link pointing at the wrong owner's repository.
func TestCheckVCSProbesTheSiteOrganisation(t *testing.T) {
	var (
		mu     sync.Mutex
		probed []string
	)
	vcs := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			probed = append(probed, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
	defer vcs.Close()

	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Host = "https://example.com"
	config.Conf.X.RepoPath = vcs.URL + "/rukins"
	config.Conf.Hosts = map[string]config.SiteOverride{
		"examplepkg.com": {RepoPath: vcs.URL + "/examplepkg"},
	}

	store := &recordingStore{}
	s := &server{db: store}

	for _, tt := range []struct {
		requestHost string
		wantPath    string
		wantHost    string
	}{
		{"example.com", "/rukins/thing", "example.com"},
		{"examplepkg.com", "/examplepkg/thing", "examplepkg.com"},
		// A Host header naming no configured site falls back to the
		// primary one rather than opening a namespace of its own.
		{"evil.example", "/rukins/thing", "example.com"},
	} {
		t.Run(tt.requestHost, func(t *testing.T) {
			mu.Lock()
			probed = nil
			mu.Unlock()
			store.saved = nil

			site := config.Conf.SiteFor(tt.requestHost)
			r, err := s.checkvcs(context.Background(), site, "thing")
			if err != nil {
				t.Fatal(err)
			}

			mu.Lock()
			got := append([]string(nil), probed...)
			mu.Unlock()
			if len(got) != 1 || got[0] != tt.wantPath {
				t.Fatalf("probed %v, want %v", got, tt.wantPath)
			}
			if r.URL != vcs.URL+tt.wantPath {
				t.Errorf("link URL = %q, want %q", r.URL, vcs.URL+tt.wantPath)
			}
			// The row must belong to the site that was asked for.
			if len(store.saved) != 1 || store.saved[0].Host != tt.wantHost {
				t.Fatalf("stored %+v, want a link on %v", store.saved, tt.wantHost)
			}
			if store.saved[0].Alias != "thing" {
				t.Errorf("stored alias = %q", store.saved[0].Alias)
			}
		})
	}
}

// TestXHandlerUsesTheSiteRepository checks the go-import meta tag names
// the repository of the site that was asked for. Getting this wrong sends
// `go get` to another organisation.
func TestXHandlerUsesTheSiteRepository(t *testing.T) {
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

	s := &server{}
	h := s.xHandler()

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
			if !contains(body, tt.want) {
				t.Fatalf("go-import does not name the site repository.\n want %v\n body %v",
					tt.want, body)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// TestLegalPagesBelongToTheirSite is the reason the legal pages do not
// follow a process onto another domain.
//
// An impressum is a statement of who operates a site, and a privacy
// policy names the party responsible for the data. Serving example.com's
// under examplepkg.com published one operator's name and postal address on
// another's domain. Suppressing the links was not enough: the routes
// themselves answered.
func TestLegalPagesBelongToTheirSite(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })

	config.Conf.Host = "https://example.com"
	config.Conf.GDPR.Impressum.Enable = true
	config.Conf.GDPR.Impressum.Content = "<p>Operator, Some Street</p>"
	config.Conf.GDPR.Privacy.Enable = true
	config.Conf.GDPR.Privacy.Content = "<p>Privacy</p>"
	config.Conf.GDPR.Contact.Enable = true
	config.Conf.GDPR.Contact.Email = "hi@example.test"
	config.Conf.Hosts = map[string]config.SiteOverride{
		"examplepkg.com": {Legal: false},
		"rukins.dev":     {Legal: true},
	}

	s := &server{}
	for _, page := range []string{".impressum", ".privacy", ".contact"} {
		for _, tt := range []struct {
			host  string
			serve bool
		}{
			{"example.com", true},
			{"rukins.dev", true},      // same operator, opted in
			{"examplepkg.com", false}, // different site
		} {
			t.Run(tt.host+page, func(t *testing.T) {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodGet, "/s/"+page, nil)
				r.Host = tt.host
				if err := s.serveStatic(context.Background(), w, r, "/s/"); err != nil {
					t.Fatal(err)
				}

				body := w.Body.String()
				switch {
				case tt.serve && w.Code != http.StatusOK:
					t.Fatalf("status %d, want the page to be served", w.Code)
				case !tt.serve && w.Code != http.StatusNotFound:
					t.Fatalf("status %d, want 404: this site does not carry %v",
						w.Code, page)
				}
				if !tt.serve && contains(body, "Some Street") {
					t.Fatal("another site's operator address was published here")
				}
			})
		}
	}
}

// TestServedPagesUseTheSharedLayout checks the server-rendered pages come
// out of one design rather than five copies of a stylesheet.
//
// They used to each carry their own, which had drifted: three different
// background colours and two different link colours across five files,
// and a footer hard-coding "/s/" in every one.
func TestServedPagesUseTheSharedLayout(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.Host = "https://example.com"
	config.Conf.S.Prefix = "/s/"
	config.Conf.GDPR.Impressum.Enable = true
	config.Conf.GDPR.Impressum.Content = "<h1>Impressum</h1>"
	config.Conf.GDPR.Privacy.Enable = true
	config.Conf.GDPR.Privacy.Content = "<h1>Privacy</h1>"
	config.Conf.GDPR.Contact.Enable = true
	config.Conf.GDPR.Contact.Email = "hi@example.test"

	s := &server{}
	for _, page := range []string{".impressum", ".privacy", ".contact"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/s/"+page, nil)
		r.Host = "example.com"
		if err := s.serveStatic(context.Background(), w, r, "/s/"); err != nil {
			t.Fatal(err)
		}

		body := w.Body.String()
		for _, want := range []string{
			"--page: #f9f9f7", // the shared design's ground, with a dark one beside it
			"prefers-color-scheme: dark",
			"example.com", // the site names itself
			`class="foot"`,
		} {
			if !contains(body, want) {
				t.Errorf("%v is missing %q", page, want)
			}
		}
		// The prefix is configurable, so no page may assume it.
		if contains(body, `href="/s/.impressum"`) && config.Conf.S.Prefix != "/s/" {
			t.Errorf("%v hard-codes a prefix", page)
		}
	}
}

// TestWarnPageNamesItsTarget checks the interstitial says where it is
// about to send someone, which is the entire reason it exists.
func TestWarnPageNamesItsTarget(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved })
	config.Conf.S.Prefix = "/s/"
	config.Conf.GDPR.Owner.Name = "example.com"

	var buf bytes.Buffer
	err := warnTmpl.ExecuteTemplate(&buf, "layout", &pageInfo{
		Site:      "example.com",
		Prefix:    "/s/",
		OwnerName: "example.com",
		URL:       "https://elsewhere.test/somewhere",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !contains(body, "https://elsewhere.test/somewhere") {
		t.Fatal("the warning does not name the target")
	}
	if !contains(body, "example.com") {
		t.Error("the warning does not name the site being left")
	}
}
