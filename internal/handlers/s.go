package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/handlers/auth"
	"github.com/rukins/gore/internal/models"
	"github.com/rukins/gore/internal/short"
	"github.com/rukins/gore/internal/utils"
)

// sHandler redirects the current request to a known link if the alias is
// found in the gore store.
func (s *service) SHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// for development.
		if config.Conf.CORS {
			log.Println("CORS is activated.")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}

		switch r.Method {
		case http.MethodOptions:
			// nothing, really.
		case http.MethodPost:
			// Creating and editing links is the console's API. 
			// It is served on the console prefix, not here.
			http.NotFound(w, r)
		case http.MethodGet:
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Cache-Control", "max-age=0")
			s.sHandlerGet(w, r)
		default:
			err := fmt.Errorf("%s is not supported", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(err.Error()))
		}
	})
}

type shortInput struct {
	Op    short.Op `json:"op"`
	Alias string   `json:"alias"`
	Data  any      `json:"data"`
}

type shortOutput struct {
	Message string `json:"message,omitempty"`
	Alias   string `json:"alias,omitempty"`
}

// sHandlerPost creates, updates and deletes links. The console prefix
// is what serves it. One route carries every operator, because the
// console is the only caller and a proxy matches the prefix once.
func (s *service) sHandlerPost(w http.ResponseWriter, r *http.Request) {
	var err error
	defer func() {
		if err == nil {
			return
		}
		b, _ := json.Marshal(shortOutput{
			Message: err.Error(),
		})
		// A refused change is the caller's mistake and is answered 400.
		// The status has to be sent before the body: it used to follow
		// it, where it has no effect, so every refusal arrived as 200.
		// An unauthorized request has already been answered by
		// handleAuth, with a redirect to the login or a status of its own.
		if !errors.Is(err, auth.ErrUnauthorized) {
			w.WriteHeader(http.StatusBadRequest)
		}
		_, _ = w.Write(b)
	}()

	// All post request must be authenticated.
	user, err := s.handleAuth(w, r)
	if err != nil {
		return
	}

	w.Header().Add("Content-Type", "application/json")

	// Decode request body and determine what is the operator
	d := json.NewDecoder(r.Body)
	var red shortInput
	err = d.Decode(&red)
	if err != nil {
		return
	}

	// Validating the operator and decode the gore data
	if !short.Op(red.Op).Valid() {
		err = errors.New("unsupported operator")
		return
	}

	b, err := json.Marshal(red.Data)
	if err != nil {
		return
	}

	var link models.Link
	err = json.Unmarshal(b, &link)
	if err != nil {
		return
	}
	link.UpdatedBy = user

	// The console sends the whole record, because its form is populated
	// from the stored one and submits every field. Nothing is left for
	// the update to preserve.
	all := short.Given{URL: true, Private: true, Trust: true, ValidFrom: true}

	// Edit redirect data.
	err = short.Edit(r.Context(), s.db, short.Op(red.Op), red.Alias, &link, all)
	if err != nil {
		return
	}
	// Flush the cache so that the changes can be effected immediately.
	s.cache.Flush()

	// A create may have chosen the alias. The body names it, and carries
	// no message: a message is how a refusal is told apart from success.
	if short.Op(red.Op) != short.OpCreate {
		return
	}
	b, err = json.Marshal(shortOutput{Alias: link.Alias})
	if err != nil {
		return
	}
	_, _ = w.Write(b)
}

// sHandlerGet is the core of gore service. It redirects a given
// alias to the actual destination.
func (s *service) sHandlerGet(w http.ResponseWriter, r *http.Request) {
	var err error
	defer func() {
		// Just redirect the user we could not find the record rather than
		// throw 50x. The server logs should be able to identify the issue.
		// A refused login has already written its own answer.
		answerErr(w, r, err)
	}()

	ctx := r.Context()

	// statistic page
	prefix := config.Conf.S.Prefix

	// URLs with /s/.* is reserved for internal usage.
	if strings.HasPrefix(r.URL.Path, prefix+".") {
		err = s.serveStatic(ctx, w, r)
		return
	}

	// Identify the alias of the short link.
	alias := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/")

	// If alias is empty, then process index page request.
	if alias == "" {
		err = s.sIndex(ctx, w, r)
		return
	}

	// Only allow valid aliases.
	if !short.Validity.MatchString(alias) {
		err = short.ErrInvalidAlias
		return
	}

	// Process visitor information, wait maximum 5 seconds.
	if config.Conf.Stats.Enable {
		recordCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		s.recognizeVisitor(recordCtx, w, r, alias)
	}

	// Figure out redirect location
	site := config.Conf.SiteFor(r.Host)
	host := site.Host
	red, ok := s.cache.Get(host, alias)
	if !ok {
		red, err = s.checkdb(ctx, host, alias)
		if err != nil {
			red, err = s.checkvcs(ctx, site, alias)
			if err != nil {
				return
			}
		}
		s.cache.Put(host, alias, red)
	}

	// Send a wait page if time does not permitting
	if time.Now().UTC().Sub(red.ValidFrom.UTC()) < 0 {
		err = s.wait.ExecuteTemplate(w, "layout", &pageInfo{
			Site:          r.Host,
			Prefix:        config.Conf.S.Prefix,
			ValidFrom:     red.ValidFrom.UTC().Format("2006-01-02T15:04:05"),
			ShowImpressum: site.ShowImpressum,
			ShowPrivacy:   site.ShowPrivacy,
			ShowContact:   site.ShowContact,
		})
		return
	}

	// Send a warn page if the redirected link is an external link
	//
	// If the link configuring person thinks the redirected link is trustable,
	// do the redirects always.
	if !red.Trust {
		// Figure out if the user allow redirects always
		allowRedir := false
		cookie, _ := r.Cookie(goreAllowCookie)
		if cookie != nil {
			n, _ := strconv.Atoi(cookie.Value)
			if n == 1 {
				allowRedir = true
			}
		}

		// If a redirect is accidentally configured as non-trustable,
		// but still an internal website, then we don't show the warn page.
		if !allowRedir && !strings.Contains(red.URL, r.Host) {
			err = s.warn.ExecuteTemplate(w, "layout", &pageInfo{
				Site:          r.Host,
				Prefix:        config.Conf.S.Prefix,
				OwnerName:     config.Conf.GDPR.Owner.Name,
				OwnerDomain:   config.Conf.GDPR.Owner.Domain,
				URL:           red.URL,
				ShowImpressum: site.ShowImpressum,
				ShowPrivacy:   site.ShowPrivacy,
				ShowContact:   site.ShowContact,
			})
			return
		}
	}

	// If this is a page that refers to a PDF, we prefer serve it as a PDF
	// content directly rather than redirect.
	if strings.HasSuffix(red.URL, ".pdf") {
		var resp *http.Response
		resp, err = http.Get(red.URL)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		_, err = io.Copy(w, resp.Body)
		return
	}

	// Finally, let's redirect!
	http.Redirect(w, r, red.URL, http.StatusTemporaryRedirect)
}

type pageInfo struct {
	OwnerName   string
	OwnerDomain string
	URL         string
	ValidFrom   string
	// Site and Prefix let the layout name the site and build its own
	// links, rather than hard-coding "/s/" as five templates did.
	Site          string
	Prefix        string
	Body          template.HTML
	Email         string
	ShowImpressum bool
	ShowPrivacy   bool
	ShowContact   bool
}

const (
	goreVidCookie   = "gore_vid"
	goreAllowCookie = "gore_allow"
)

// recognizeVisitor implements a best effort visitor recording.
//
// If the gore cookie is presented, then we use cookie id.
// If the cookie does not present any data, we read the IP address, and
// allocates a new visitor id for the visitor.
//
// We don't care if any error happens inside.
func (s *service) recognizeVisitor(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	alias string,
) {
	host := config.Conf.ResolveHost(r.Host)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var cookieVid string
	c, err := r.Cookie(goreVidCookie)
	if err != nil {
		cookieVid = ""
	} else {
		cookieVid = c.Value
	}

	// count visit and set cookie.
	vid, err := s.db.RecordVisit(ctx, &models.Visit{
		VisitorID: cookieVid,
		Host:      host,
		Alias:     alias,
		IP:        utils.ReadIP(r),
		UA:        r.UserAgent(),
		Referer:   r.Referer(),
		Time:      time.Now().UTC(),
	})
	if err != nil {
		log.Printf("cannot record alias %s's visit: %v", alias, err)
	} else {
		// Path matters here. The cookie used to be written as a raw
		// header with no attributes, so it defaulted to the path of the
		// request, /s/<alias>. A visitor arriving at a second alias
		// therefore never sent it back and was issued another identity:
		// 277,318 of 348,356 recorded visits invented a visitor. Scoping
		// it to the site makes it identify a visitor rather than a
		// visit.
		//
		// It stays a session cookie. Giving it a lifetime would change
		// what the numbers mean, and UV counts addresses today.
		http.SetCookie(w, &http.Cookie{
			Name:     goreVidCookie,
			Value:    vid,
			Path:     "/",
			HttpOnly: true,
			Secure:   !config.Conf.Development,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

// checkdb checks whether the given alias is exsited in the gore database
func (s *service) checkdb(ctx context.Context, host, alias string) (*models.Link, error) {
	a, err := s.db.FetchAlias(ctx, host, alias)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// checkvcs checks whether the given alias is an repository on VCS, if so,
// then creates a new alias and returns url of the vcs repository.
func (s *service) checkvcs(ctx context.Context, site config.Site, alias string) (*models.Link, error) {
	host := site.Host

	// The organisation probed is the one belonging to the site the
	// request arrived on, not the process's primary site. A miss on
	// examplepkg,com must look for examplepkg/<alias> and never
	// examplep/<alias>, because a 200 here creates a link.
	repoPath := site.RepoPath
	repoPath = strings.TrimSuffix(repoPath, "/*")
	tryPath := fmt.Sprintf("%s/%s", repoPath, alias)
	resp, err := http.Get(tryPath)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusMovedPermanently {
		return nil, fmt.Errorf("%s is not a repository", tryPath)
	}

	// figure out the new location
	if resp.StatusCode == http.StatusMovedPermanently {
		tryPath = resp.Header.Get("Location")
	}

	// store such a try path
	r := &models.Link{
		Host:      host,
		Alias:     alias,
		URL:       tryPath,
		Private:   false,
		Trust:     false,
		ValidFrom: time.Now().UTC(),
	}
	err = s.db.StoreAlias(ctx, r)
	if err != nil {
		return s.checkdb(ctx, host, alias)
	}

	return r, nil
}

var (
	errInvalidStatParam = errors.New("invalid stat parameter")
	errMissingStatParam = errors.New("missing stat parameter")
)

// answerErr turns a failed lookup into a redirect. The public pages do
// that rather than answer 500; the log keeps the cause. A refused login
// has already written its own response and must not be replaced.
func answerErr(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil && !errors.Is(err, auth.ErrUnauthorized) {
		log.Printf("request err: %v\n", err)
		http.Redirect(w, r, "/404.html", http.StatusTemporaryRedirect)
	}
}

// dashboardView is what the console template reads off #root.
// ShortPrefix is the shortener, not the page's own mount: the console
// is served elsewhere, and a link copied from it still has to be /s/blog.
type dashboardView struct {
	AdminView     bool
	StatsMode     bool
	DevMode       bool
	ShowImpressum bool
	ShowPrivacy   bool
	ShowContact   bool
	LogoutURL     string
	ShortPrefix   string
}

func newDashboardView(r *http.Request, admin bool) dashboardView {
	// The legal pages name an operator, so they belong to the site that
	// was asked for rather than to the process.
	site := config.Conf.SiteFor(r.Host)
	return dashboardView{
		AdminView:     admin,
		StatsMode:     config.Conf.Stats.Enable,
		DevMode:       config.Conf.Development,
		ShowImpressum: site.ShowImpressum,
		ShowPrivacy:   site.ShowPrivacy,
		ShowContact:   site.ShowContact,
		// Only a real session can be ended. Under basic auth the dashboard
		// keeps its old reload-the-page behaviour.
		LogoutURL:   logoutURL(),
		ShortPrefix: config.Conf.S.Prefix,
	}
}

// sIndex serves the public index. No mode renders the page. mode=index
// is its listing. The console and its documents are not modes of this
// page: a query here cannot be kept off the public router, and a
// redirect would publish the console's prefix.
func (s *service) sIndex(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) error {
	switch r.URL.Query().Get("mode") {
	case "":
		// The public index, rendered below.
	case "index":
		return s.indexData(ctx, w, r, true)
	default:
		http.NotFound(w, r)
		return nil
	}

	// Process visitor information for public index, wait maximum 5 seconds.
	recordCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.recognizeVisitor(recordCtx, w, r, "")

	w.Header().Add("Content-Type", "text/html")
	return s.dashboard.Execute(w, newDashboardView(r, false))
}

// indexOutput is the link listing. Series carries one entry per alias on
// the page, and is absent from the public listing, which draws none.
// indexOutput is the link listing. Series carries one entry per alias on
// the page, and is absent from the public listing, which draws none.
type indexOutput struct {
	Data   []models.LinkIndex `json:"data"`
	Page   int64              `json:"page"`
	Total  int64              `json:"total"`
	Series map[string][]int64 `json:"series,omitempty"`
}

// index on all aliases, require admin access.
func (s *service) indexData(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	public bool,
) error {
	if !public {
		_, err := s.handleAuth(w, r)
		if err != nil {
			return err
		}
	}
	w.Header().Add("Content-Type", "application/json")

	// get page size and number
	ps := r.URL.Query().Get("ps")
	pageSize, err := strconv.ParseUint(ps, 10, 0)
	if err != nil {
		pageSize = 5
	}
	pn := r.URL.Query().Get("pn")
	pageNum, err := strconv.ParseUint(pn, 10, 0)
	if err != nil || pageNum <= 0 {
		pageNum = 1
	}

	host := config.Conf.ResolveHost(r.Host)
	rs, total, err := s.db.FetchAliasAll(ctx, host,
		public, int64(pageSize), int64(pageNum))
	if err != nil {
		return err
	}

	out := indexOutput{
		Data:  rs,
		Page:  int64(pageNum),
		Total: total,
	}

	// The console draws a sparkline on every row. Fetching one per row
	// would make a page of twenty links twenty round trips for a handful
	// of numbers each, so the series for the whole page comes back with
	// it. The public index draws no sparkline and is not charged for one.
	if !public && len(rs) > 0 {
		aliases := make([]string, 0, len(rs))
		for _, r := range rs {
			aliases = append(aliases, r.Alias)
		}
		start, end := sparkRange()
		series, err := s.db.StatDaily(ctx, host, aliases, start, end)
		if err != nil {
			return err
		}
		out.Series = fillSeries(series, start, end)
	}

	b, err := json.Marshal(out)
	if err != nil {
		return err
	}

	_, _ = w.Write(b)
	return nil
}

// sparkDays is how far back a row's sparkline reaches. Two weeks is
// enough to show a shape without making the bars unreadably thin.
const sparkDays = 14

func sparkRange() (start, end time.Time) {
	end = time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	return end.AddDate(0, 0, -sparkDays), end
}

// fillSeries turns the store's sparse days into one value per day, so the
// dashboard draws a fixed number of bars and a quiet day is a gap in the
// shape rather than a missing bar that shifts everything after it.
func fillSeries(
	series map[string][]models.DayCount,
	start, end time.Time,
) map[string][]int64 {
	days := make([]string, 0, sparkDays)
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format(time.DateOnly))
	}

	out := make(map[string][]int64, len(series))
	for alias, counts := range series {
		byDay := make(map[string]int64, len(counts))
		for _, c := range counts {
			byDay[c.Day] = c.PV
		}
		row := make([]int64, len(days))
		for i, d := range days {
			row[i] = byDay[d]
		}
		out[alias] = row
	}
	return out
}

// overviewData answers the console's first question, whether anything is
// happening, in one request. Adding these up from the link listing would
// mean paging through every link.
func (s *service) overviewData(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) error {
	if _, err := s.handleAuth(w, r); err != nil {
		return err
	}
	w.Header().Add("Content-Type", "application/json")

	start, end, err := parseDuration(r.URL.Query())
	if err != nil {
		return fmt.Errorf("%w: %v", errInvalidStatParam, err)
	}

	o, err := s.db.StatOverview(ctx, config.Conf.ResolveHost(r.Host), start, end)
	if err != nil {
		return err
	}
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, _ = w.Write(b)
	return nil
}

func (s *service) statData(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) (retErr error) {
	defer func() {
		if retErr != nil {
			retErr = fmt.Errorf("%w: %v", errInvalidStatParam, retErr)
		}
	}()

	params := r.URL.Query()
	a := params.Get("a")
	if a == "" {
		retErr = fmt.Errorf("%s: alias (a)", errMissingStatParam)
		return
	}

	stat := params.Get("stat")
	if stat == "" {
		retErr = fmt.Errorf("%s: stat mode (stat)", errMissingStatParam)
		return
	}

	start, end, err := parseDuration(params)
	if err != nil {
		retErr = fmt.Errorf("%s: %v", errInvalidStatParam, err)
		return
	}

	w.Header().Add("Content-Type", "application/json")

	host := config.Conf.ResolveHost(r.Host)
	var results any
	switch stat {
	// The grouped modes share one query. Everything they draw excludes
	// bots, so the figures on the page count the same population.
	case "referer", "browser", "os", "device":
		results, err = s.db.StatGroup(ctx, host, a, stat, start, end)
	case "bots":
		results, err = s.db.StatBots(ctx, host, a, start, end)
	case "ua":
		results, err = s.db.StatUA(ctx, host, a, start, end)
	case "time":
		results, err = s.db.StatVisitHist(ctx, host, a, start, end)
	default:
		retErr = fmt.Errorf("%s stat mode is not supported", stat)
		return
	}
	if err != nil {
		retErr = err
		return
	}

	b, err := json.Marshal(results)
	if err != nil {
		retErr = err
		return
	}
	_, _ = w.Write(b)
	return
}

func parseDuration(p url.Values) (start, end time.Time, err error) {
	t0 := p.Get("t0")
	if t0 != "" {
		start, err = time.Parse("2006-01-02", t0)
		if err != nil {
			return
		}
	} else {
		start = time.Now().UTC().Add(-time.Hour * 24 * 7) // last week
	}
	t1 := p.Get("t1")
	if t1 != "" {
		end, err = time.Parse("2006-01-02", t1)
		if err != nil {
			return
		}
	} else {
		end = time.Now().UTC()
	}
	return
}
