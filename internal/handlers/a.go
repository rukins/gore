package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/rukins/gore/internal/config"
)

// AHandler is the operator console and the API it reads. Everything it
// serves sits under the console prefix, so one proxy rule covers the
// page, its assets, its login and its documents.
func (s *service) AHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.Conf.CORS {
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}

		switch r.Method {
		case http.MethodOptions:
		case http.MethodPost:
			s.sHandlerPost(w, r)
		case http.MethodGet:
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Cache-Control", "max-age=0")
			s.aHandlerGet(w, r)
		default:
			err := fmt.Errorf("%s is not supported", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(err.Error()))
		}
	})
}

func (s *service) aHandlerGet(w http.ResponseWriter, r *http.Request) {
	var err error
	defer func() { answerErr(w, r, err) }()

	prefix := config.Conf.AdminPrefix()
	if strings.HasPrefix(r.URL.Path, prefix+".") {
		err = s.serveAdminStatic(w, r, prefix)
		return
	}
	// The console has no aliases. Anything past the prefix is not a page.
	if strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/") != "" {
		http.NotFound(w, r)
		return
	}
	err = s.aIndex(r.Context(), w, r)
}

// aIndex is the console. No mode renders the page. mode selects one of
// the documents the page fetches, all of them after a session, so the
// private prefix is the whole operator surface and not only the HTML.
func (s *service) aIndex(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) error {
	if _, err := s.handleAuth(w, r); err != nil {
		return err
	}	
	switch r.URL.Query().Get("mode") {
	case "":
		w.Header().Add("Content-Type", "text/html")
		return s.dashboard.Execute(w, newDashboardView(r, true))
	case "index-pro":
		return s.indexData(ctx, w, r, false)
	case "overview":
		return s.overviewData(ctx, w, r)
	case "stats":
		if !config.Conf.Stats.Enable {
			http.NotFound(w, r)
			return nil
		}
		return s.statData(ctx, w, r)
	default:
		http.NotFound(w, r)
		return nil
	}
}
