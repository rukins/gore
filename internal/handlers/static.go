package handlers

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/handlers/auth"
)

func (s *service) serveStatic(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) error {
	prefix := config.Conf.S.Prefix

	site := config.Conf.SiteFor(r.Host)

	// Login lives on the console prefix. A dotted path here is a static
	// file or a legal page.
	var (
		t *template.Template
		d *pageInfo
	)
	switch {
	case strings.HasPrefix(r.URL.Path, prefix+".static"):
		// Serve static files under ./.static/*. This should not conflict
		// with all existing aliases, meaning that alias should not start
		// with a dot.
		return s.serveAsset(w, r, prefix)
	case strings.HasPrefix(r.URL.Path, prefix+".impressum"):
		if site.ShowImpressum {
			t = s.impressum
		}
		d = &pageInfo{
			Body:          template.HTML(config.Conf.GDPR.Impressum.Content),
			ShowImpressum: site.ShowImpressum,
			ShowPrivacy:   site.ShowPrivacy,
			ShowContact:   site.ShowContact,
		}
	case strings.HasPrefix(r.URL.Path, prefix+".privacy"):
		if site.ShowPrivacy {
			t = s.privacy
		}
		d = &pageInfo{
			Body:          template.HTML(config.Conf.GDPR.Privacy.Content),
			ShowImpressum: site.ShowImpressum,
			ShowPrivacy:   site.ShowPrivacy,
			ShowContact:   site.ShowContact,
		}
	case strings.HasPrefix(r.URL.Path, prefix+".contact"):
		if site.ShowContact {
			t = s.contact
		}
		d = &pageInfo{
			Email:         config.Conf.GDPR.Contact.Email,
			ShowImpressum: site.ShowImpressum,
			ShowPrivacy:   site.ShowPrivacy,
			ShowContact:   site.ShowContact,
		}
	}
	if t != nil {
		d.Site = r.Host
		d.Prefix = prefix
		return t.ExecuteTemplate(w, "layout", d)
	}
	// A site that does not carry these pages has no such route. Saying so
	// is better than an empty 200, which reads as a page that is merely
	// blank.
	http.NotFound(w, r)
	return nil
}

// serveAdminStatic answers the console's login flow and its assets.
// Legal pages stay on the shortener; they are public.
func (s *service) serveAdminStatic(w http.ResponseWriter, r *http.Request, prefix string) error {
	if routes, ok := s.latere.(auth.Router); ok && routes.Route(w, r) {
		return nil
	}
	if strings.HasPrefix(r.URL.Path, prefix+".static") {
		return s.serveAsset(w, r, prefix)
	}
	http.NotFound(w, r)
	return nil
}

// serveAsset writes one file of the dashboard bundle. prefix is the mount
// the page was served on, so /s/.static and /a/.static share one tree.
func (s *service) serveAsset(w http.ResponseWriter, r *http.Request, prefix string) error {
	switch filepath.Ext(r.URL.Path) {
	case ".css":
		w.Header().Add("Content-Type", "text/css")
	case ".js":
		w.Header().Add("Content-Type", "text/javascript")
	}

	f, err := s.statics.Open(strings.TrimPrefix(r.URL.Path, prefix+".static/"))
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
