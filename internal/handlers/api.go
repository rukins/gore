package handlers

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/rukins/gore/internal/cache"
	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/db"
	"github.com/rukins/gore/internal/handlers/auth"
)

type GoreAPI interface {
	Close() error

	SHandler() http.HandlerFunc
	XHandler() http.HandlerFunc
	AHandler() http.HandlerFunc
}

type Assets struct {
	Pages     fs.FS
	Dashboard fs.FS
}

type service struct {
	db     db.Store
	cache  *cache.LRU
	basic  auth.Auth
	latere auth.Auth // nil unless auth.enable is latere and it is configured

	x, wait, warn, impressum, privacy, contact, dashboard *template.Template
	statics                                               fs.FS
}

func (s *service) Close() error {
	return s.db.Close()
}

func NewGoreAPI(ctx context.Context, a Assets) GoreAPI {
	// A bad embed should fail before a database connection is attempted.
	dashboard, statics, err := prepareDashboard(a.Dashboard)
	if err != nil {
		log.Fatalf("cannot prepare dashboard: %v", err)
	}
	imp, err := prepareImportPage(a.Pages)
	if err != nil {
		log.Fatalf("cannot prepare import page: %v", err)
	}

	// Opening the store applies pending migrations, which is more than a
	// connect, so this allows more than the second a connect would need.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, err := db.NewStore(ctx, config.Conf.Store)
	if err != nil {
		log.Fatalf("cannot establish connection to %s, details: \n%v",
			db.Redact(config.Conf.Store), err)
	}

	basic := auth.NewBasicAuth()

	var latere auth.Auth
	if config.Conf.A.Enable && config.Conf.Auth.Enable == config.Latere {
		latere = auth.NewLatereAuth(config.Conf.AdminPrefix())
	}

	// The rest share one layout, so the design lives in a single file
	// instead of five copies of a stylesheet that had drifted apart.
	page := func(name string) *template.Template {
		t := template.Must(template.New(name).ParseFS(a.Pages, "templates/base.html"))
		return template.Must(t.ParseFS(a.Pages, name))
	}

	return &service{
		db:     store,
		cache:  cache.NewLRU(true),
		basic:  basic,
		latere: latere,

		x:         imp,
		wait:      page("templates/wait.html"),
		warn:      page("templates/warn.html"),
		impressum: page("templates/impressum.html"),
		privacy:   page("templates/privacy.html"),
		contact:   page("templates/contact.html"),
		dashboard: dashboard,

		statics: statics,
	}
}

// prepareDashboard loads the dashboard page and the files it links to
func prepareDashboard(dashboard fs.FS) (page *template.Template, statics fs.FS, err error) {
	raw, err := fs.ReadFile(dashboard, "dashboard/build/index.html")
	if err != nil {
		return nil, nil, err
	}
	html := strings.ReplaceAll(string(raw), "/static", "./.static")
	page, err = template.New("dashboard").Parse(html)
	if err != nil {
		return nil, nil, err
	}

	statics, err = fs.Sub(dashboard, "dashboard/build/static")
	if err != nil {
		return nil, nil, err
	}
	// Sub does not check that the directory exists. An empty or missing
	// tree would otherwise boot, and the console would be a blank page.
	if _, err = fs.Stat(statics, "."); err != nil {
		return nil, nil, fmt.Errorf("dashboard static files: %w", err)
	}
	return page, statics, nil
}

// prepareImportPage loads the go-import document served under /x/.
//
// It is not a page. `go get` reads the meta tag and never renders it, so
// it is parsed alone rather than into the shared layout. Execute serves
// this template directly, which only carries the document when the file
// itself is the template body.
func prepareImportPage(pages fs.FS) (*template.Template, error) {
	raw, err := fs.ReadFile(pages, "templates/x.html")
	if err != nil {
		return nil, err
	}
	return template.New("x").Parse(string(raw))
}
