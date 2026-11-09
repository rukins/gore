package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/rukins/gore/internal/config"
)

// xHandler redirect returns an HTTP handler that redirects requests for
// the tree rooted at importPath to pkg.go.dev pages for those import paths.
// The redirections include headers directing `go get.` to satisfy the
// imports by checking out code from repoPath using the configured VCS.
func (s *service) XHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// The import path comes from the request, and the repository
		// root from the site it belongs to: examplepkg.com/x/foo must
		// resolve to examplepkg.com/foo, not example.com/foo.
		site := config.Conf.SiteFor(req.Host)
		importPath := strings.TrimSuffix(req.Host+config.Conf.X.Prefix, "/")
		path := strings.TrimSuffix(req.Host+req.URL.Path, "/")
		var importRoot, repoRoot, suffix string
		if path == importPath {
			http.Redirect(w, req, config.Conf.X.GoDocHost+importPath, http.StatusFound)
			return
		}
		elem := path[len(importPath)+1:]
		if i := strings.Index(elem, "/"); i >= 0 {
			elem, suffix = elem[:i], elem[i:]
		}
		importRoot = importPath + "/" + elem
		repoRoot = site.RepoPath + "/" + elem

		// Handling 'git clone https://example.com/x/repo'.
		if suffix == "/info/refs" && strings.HasPrefix(req.URL.Query().Get("service"), "git-") && elem != "" {
			http.Redirect(w, req, fmt.Sprintf("%s/info/refs?%s", repoRoot, req.URL.RawQuery), http.StatusFound)
			return
		}

		d := &struct {
			ImportRoot string
			VCS        string
			VCSRoot    string
			Suffix     string
		}{
			ImportRoot: importRoot,
			VCS:        config.Conf.X.VCS,
			VCSRoot:    repoRoot,
			Suffix:     suffix,
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		err := s.x.Execute(w, d)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
}
