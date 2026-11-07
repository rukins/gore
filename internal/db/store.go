package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rukins/gore/internal/db/migrate"
	"github.com/rukins/gore/internal/db/postgres"
	"github.com/rukins/gore/internal/db/sqlite"
	"github.com/rukins/gore/internal/models"
)

// ErrAliasExists reports that (host, alias) is taken, so a caller can
// tell "already there" from "broken" without matching on message text.
// Both engines return this value. Compare it directly; it is not wrapped.
var ErrAliasExists = migrate.ErrAliasExists

// Store is the persistence gore runs on.
//
// PostgreSQL and SQLite both implement it. The interface is also the seam
// the handler tests substitute, so recording a visit and setting its cookie
// can be tested without a database.
//
// A link is identified by the pair (host, alias): one process serves
// several hosts and the same alias means different things on each. Methods
// that take a whole model read the host from it; the others take it as an
// argument.
type Store interface {
	// Close releases the connection.
	Close() error

	// StoreAlias stores r if (r.Host, r.Alias) is free, and reports an
	// error if it is taken.
	StoreAlias(ctx context.Context, r *models.Link) error
	// UpdateAlias updates the link identified by r.ID.
	UpdateAlias(ctx context.Context, r *models.Link) error
	// DeleteAlias removes a link. Its visits are kept: they happened.
	DeleteAlias(ctx context.Context, host, alias string) error
	// FetchAlias returns one link.
	FetchAlias(ctx context.Context, host, alias string) (*models.Link, error)
	// FetchAliasAll returns a page of links and the total count. The
	// public form omits the target URL and the visit counts.
	FetchAliasAll(ctx context.Context, host string, public bool, pageSize, pageNum int64) ([]models.LinkIndex, int64, error)

	// RecordVisit records one visit and returns the visitor id to set as
	// a cookie.
	RecordVisit(ctx context.Context, v *models.Visit) (string, error)

	// StatGroup counts non-bot visits within [start, end), grouped by one
	// derived column: referer, browser, os or device.
	StatGroup(ctx context.Context, host, alias, by string, start, end time.Time) ([]models.NameCount, error)
	// StatBots reports the automated traffic the other statistics leave
	// out, so the exclusion is visible rather than silent.
	StatBots(ctx context.Context, host, alias string, start, end time.Time) (models.BotStat, error)
	// StatUA counts visits per raw user agent within [start, end), bots
	// included. Nothing draws it; it is how a suspicious entry in the
	// grouped statistics is inspected.
	StatUA(ctx context.Context, host, alias string, start, end time.Time) ([]models.UAStat, error)
	// StatVisitHist counts non-bot PV and UV per hour within [start, end).
	StatVisitHist(ctx context.Context, host, alias string, start, end time.Time) ([]models.TimeHist, error)
	// StatOverview totals one site: its links, its traffic, and how much
	// of that traffic was a person.
	StatOverview(ctx context.Context, host string, start, end time.Time) (models.Overview, error)
	// StatDaily returns a daily non-bot count per alias, in one query, so
	// a page of rows costs one round trip rather than one each.
	StatDaily(ctx context.Context, host string, aliases []string, start, end time.Time) (map[string][]models.DayCount, error)
	// StatVisit counts PV and UV for the given aliases.
	StatVisit(ctx context.Context, host string, aliases []string) ([]models.VisitRecord, error)
}

// NewStore opens the store named by uri.
func NewStore(ctx context.Context, uri string) (Store, error) {
	scheme, _, ok := strings.Cut(uri, "://")
	if !ok {
		return nil, fmt.Errorf("store %q has no scheme, want postgres:// or sqlite://", Redact(uri))
	}

	switch scheme {
	case "postgres", "postgresql":
		return postgres.Open(ctx, uri)
	case "sqlite":
		return sqlite.Open(ctx, uri)
	default:
		return nil, fmt.Errorf("unsupported store scheme %q, want postgres:// or sqlite://", scheme)
	}
}

// Redact removes the password from a store URI before it reaches a log
// line or an error message, both of which are read by people who should
// not be shown the database credentials.
func Redact(uri string) string { return migrate.Redact(uri) }
