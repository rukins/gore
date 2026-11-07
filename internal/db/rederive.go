package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/rukins/gore/internal/db/postgres"
	"github.com/rukins/gore/internal/db/sqlite"
)

// Rederive recomputes the derived columns of stored visits.
//
// browser, os, device, is_bot and referer_host are a cached pure function
// of ua and referer. When that function improves, the cache is stale, and
// leaving it stale means historical rows are classified by one rule and
// new rows by another, so a chart mixes the two.
//
// It reads only ua and referer, which are the columns the derivation
// takes, and rewrites only the columns it produces. Running it twice
// changes nothing the second time, so it is safe to re-run after a rule
// changes again.
func Rederive(ctx context.Context, uri, host string) error {
	scheme, _, ok := strings.Cut(uri, "://")
	if !ok {
		return fmt.Errorf("store %q has no scheme, want postgres:// or sqlite://", Redact(uri))
	}
	switch scheme {
	case "postgres", "postgresql":
		return postgres.Rederive(ctx, uri, host)
	case "sqlite":
		return sqlite.Rederive(ctx, uri, host)
	default:
		return fmt.Errorf("unsupported store scheme %q, want postgres:// or sqlite://", scheme)
	}
}
