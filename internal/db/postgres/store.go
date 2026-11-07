package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rukins/gore/internal/db/migrate"
	"github.com/rukins/gore/internal/models"
	"uuid"
)

// store is the PostgreSQL backend.
type store struct {
	pool *pgxpool.Pool
}

// uniqueViolation is the SQLSTATE for a unique constraint breach.
const uniqueViolation = "23505"

// idAttempts is how many times an insert retries a primary-key collision.
// Two processes can pick the same microsecond. The next attempt asks for
// a new id. An alias collision is not retried.
const idAttempts = 8

type uniqueKind int

const (
	uniqueNone uniqueKind = iota
	uniqueAlias
	uniqueID
)

func (db *store) Close() error {
	db.pool.Close()
	return nil
}

func (db *store) StoreAlias(ctx context.Context, r *models.Link) error {
	// valid_from is stored as given, including the zero value. short.go
	// treats a zero time as "valid since always"; substituting the
	// current time here would make it "valid from now", which is a
	// different link.
	now := micros(time.Now().UTC())
	var err error
	for range idAttempts {
		id := migrate.NextID()
		r.ID = strconv.FormatInt(id, 10)
		_, err = db.pool.Exec(ctx, `
			INSERT INTO links
				(id, host, alias, url, private, trust, valid_from,
				 created_by, updated_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
			id, r.Host, r.Alias, r.URL, r.Private, r.Trust, micros(r.ValidFrom),
			r.CreatedBy, r.UpdatedBy, now,
		)
		if err == nil {
			return nil
		}
		switch pgUnique(err) {
		case uniqueAlias:
			return migrate.ErrAliasExists
		case uniqueID:
			continue
		}
		return fmt.Errorf("failed to insert given redirect: %w", err)
	}
	return fmt.Errorf("failed to insert given redirect: %w", err)
}

func (db *store) UpdateAlias(ctx context.Context, r *models.Link) error {
	if r.ID == "" {
		return errors.New("missing document ID")
	}
	id, err := strconv.ParseInt(r.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("unparseable id %q: %w", r.ID, err)
	}

	tag, err := db.pool.Exec(ctx, `
		UPDATE links SET
			alias = $2, url = $3, private = $4, trust = $5,
			valid_from = $6, updated_by = $7, updated_at = $8
		WHERE id = $1`,
		id, r.Alias, r.URL, r.Private, r.Trust,
		micros(r.ValidFrom), r.UpdatedBy, micros(time.Now().UTC()),
	)
	if err != nil {
		if pgUnique(err) == uniqueAlias {
			return migrate.ErrAliasExists
		}
		return fmt.Errorf("failed to update alias %s: %v", r.Alias, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("failed to update alias %s: no such link", r.Alias)
	}
	return nil
}

// DeleteAlias removes the link and leaves its visits in place. The visits
// happened, and the alias may be created again later; the stat queries
// join links, so an orphaned history reappears if it is.
func (db *store) DeleteAlias(ctx context.Context, host, alias string) error {
	_, err := db.pool.Exec(ctx,
		`DELETE FROM links WHERE host = $1 AND alias = $2`, host, alias)
	if err != nil {
		return fmt.Errorf("delete alias %s failed: %w", alias, err)
	}
	return nil
}

func (db *store) FetchAlias(ctx context.Context, host, alias string) (*models.Link, error) {
	var (
		r                           models.Link
		id                          int64
		validFrom, created, updated int64
	)
	err := db.pool.QueryRow(ctx, `
		SELECT id, host, alias, url, private, trust, valid_from,
		       created_by, updated_by, created_at, updated_at
		FROM links WHERE host = $1 AND alias = $2`, host, alias,
	).Scan(&id, &r.Host, &r.Alias, &r.URL, &r.Private, &r.Trust,
		&validFrom, &r.CreatedBy, &r.UpdatedBy, &created, &updated)
	if err != nil {
		return nil, fmt.Errorf("cannot find alias %s: %v", alias, err)
	}
	r.ID = strconv.FormatInt(id, 10)
	r.ValidFrom = fromMicros(validFrom)
	r.CreatedAt = fromMicros(created)
	r.UpdatedAt = fromMicros(updated)
	return &r, nil
}

// FetchAliasAll returns a page of links.
//
// The public form omits the URL and the counts, so that the public index
// cannot be used to enumerate targets or to read traffic figures.
//
// The counts come from a LEFT JOIN, so a link nobody has visited reports
// pv=0, uv=0 rather than being left out of the listing.
func (db *store) FetchAliasAll(
	ctx context.Context,
	host string,
	public bool,
	pageSize, pageNum int64,
) ([]models.LinkIndex, int64, error) {
	offset := (pageNum - 1) * pageSize

	var total int64
	countQuery := `SELECT COUNT(*) FROM links WHERE host = $1`
	if public {
		countQuery += ` AND NOT private`
	}
	if err := db.pool.QueryRow(ctx, countQuery, host).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("cannot count links: %w", err)
	}

	query := `
		SELECT l.id, l.host, l.alias, l.url, l.private, l.trust,
		       l.valid_from, l.created_by, l.updated_by,
		       l.created_at, l.updated_at,
		       COUNT(v.id) AS pv,
		       COUNT(DISTINCT v.ip) AS uv
		FROM links l
		LEFT JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = $1
		GROUP BY l.id
		ORDER BY l.updated_at DESC
		LIMIT $2 OFFSET $3`
	if public {
		// No join at all: the public index shows neither URL nor counts,
		// so aggregating over the visit table would be work whose result
		// is discarded.
		query = `
		SELECT id, host, alias, '', private, trust,
		       valid_from, created_by, updated_by,
		       created_at, updated_at, 0, 0
		FROM links
		WHERE host = $1 AND NOT private
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3`
	}

	rows, err := db.pool.Query(ctx, query, host, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot fetch links: %w", err)
	}
	defer rows.Close()

	var rs []models.LinkIndex
	for rows.Next() {
		var (
			r                           models.LinkIndex
			id                          int64
			validFrom, created, updated int64
		)
		if err := rows.Scan(&id, &r.Host, &r.Alias, &r.URL, &r.Private,
			&r.Trust, &validFrom, &r.CreatedBy, &r.UpdatedBy,
			&created, &updated, &r.PV, &r.UV); err != nil {
			return nil, 0, fmt.Errorf("cannot scan link: %w", err)
		}
		r.ID = strconv.FormatInt(id, 10)
		r.ValidFrom = fromMicros(validFrom)
		r.CreatedAt = fromMicros(created)
		r.UpdatedAt = fromMicros(updated)
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("cannot fetch links: %w", err)
	}
	return rs, total, nil
}

// RecordVisit stores one visit and returns the visitor id to set as a
// cookie.
//
// The incoming id is validated as a UUID rather than trusted. Production
// holds values such as "-1%20OR%202%2B471-471-1=0%2B0%2B0%2B1%20--%20",
// which are scanner probes that were stored verbatim and echoed back in
// the Set-Cookie header.
func (db *store) RecordVisit(ctx context.Context, v *models.Visit) (string, error) {
	vid, err := uuid.Parse(v.VisitorID)
	if err != nil {
		vid = uuid.New()
	}
	v.VisitorID = vid.String()
	v.Derive()

	when := v.Time
	if when.IsZero() {
		when = time.Now().UTC()
	}

	for range idAttempts {
		id := migrate.NextID()
		_, err = db.pool.Exec(ctx, `
			INSERT INTO visits
				(id, host, alias, visitor_id, ip, ua, referer,
				 referer_host, browser, os, device, is_bot, time)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			id, v.Host, v.Alias, v.VisitorID, v.IP, v.UA, v.Referer,
			v.RefererHost, v.Browser, v.OS, v.Device, v.IsBot, micros(when))
		if err == nil {
			return v.VisitorID, nil
		}
		if pgUnique(err) == uniqueID {
			continue
		}
		return "", fmt.Errorf("failed to insert record: %w", err)
	}
	return "", fmt.Errorf("failed to insert record: %w", err)
}

// nonBotVisits is the FROM clause the displayed statistics share.
//
// The join to links is not decoration. 21% of the visit rows have an alias
// that is not a link: the index page records an empty alias, and a visit
// is recorded before the alias is resolved, so 404s are counted too.
// Querying visits alone would report all of them as traffic to aliases
// that do not exist.
//
// Bots are excluded here rather than in each query, so that every figure
// on the stats page counts the same population. They were previously
// dropped from two charts and kept in the rest, which left the charts on
// one page disagreeing with each other. StatBots reports what this
// removes.
const nonBotVisits = `
	FROM links l
	JOIN visits v ON v.host = l.host AND v.alias = l.alias
	WHERE l.host = $1 AND l.alias = $2
	  AND v.time >= $3 AND v.time < $4
	  AND NOT v.is_bot`

// groupable are the columns a grouped statistic may count over.
//
// The column name is concatenated into the query, so it comes from this
// map and never from the request. The value is what an empty column
// reports as: an absent referring host is a direct visit, and an
// unidentified browser is unknown rather than blank.
var groupable = map[string]string{
	"referer": "Direct",
	"browser": "Unknown",
	"os":      "Unknown",
	"device":  "Unknown",
}

// column maps a stat mode to the column holding it. referer groups by
// host rather than by full URL, so one referring page with varying query
// parameters is one row instead of many.
var column = map[string]string{
	"referer": "referer_host",
	"browser": "browser",
	"os":      "os",
	"device":  "device",
}

// StatGroup counts non-bot visits grouped by one derived column.
func (db *store) StatGroup(
	ctx context.Context,
	host, alias, by string,
	start, end time.Time,
) ([]models.NameCount, error) {
	empty, ok := groupable[by]
	if !ok {
		return nil, fmt.Errorf("cannot group visits by %q", by)
	}

	rows, err := db.pool.Query(ctx, `
		SELECT CASE WHEN v.`+column[by]+` = '' THEN '`+empty+`'
		            ELSE v.`+column[by]+` END AS name,
		       COUNT(*) AS count`+nonBotVisits+`
		GROUP BY 1 ORDER BY count DESC, name`, host, alias, micros(start), micros(end))
	if err != nil {
		return nil, fmt.Errorf("failed to count %v: %w", by, err)
	}
	defer rows.Close()

	var rs []models.NameCount
	for rows.Next() {
		var r models.NameCount
		if err := rows.Scan(&r.Name, &r.Count); err != nil {
			return nil, fmt.Errorf("failed to fetch %v results: %w", by, err)
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to fetch %v results: %w", by, err)
	}
	return rs, nil
}

// StatBots reports the traffic the other statistics leave out.
func (db *store) StatBots(
	ctx context.Context,
	host, alias string,
	start, end time.Time,
) (models.BotStat, error) {
	var b models.BotStat
	err := db.pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT v.ip)
		FROM links l
		JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = $1 AND l.alias = $2
		  AND v.time >= $3 AND v.time < $4
		  AND v.is_bot`, host, alias, micros(start), micros(end)).Scan(&b.PV, &b.UV)
	if err != nil {
		return models.BotStat{}, fmt.Errorf("failed to count bots: %w", err)
	}
	return b, nil
}

// StatUA counts visits per raw user agent string, bots included.
//
// Nothing on the dashboard draws this. It is how a suspicious entry in the
// grouped statistics gets looked at, which needs the string the grouping
// was derived from.
func (db *store) StatUA(
	ctx context.Context,
	host, alias string,
	start, end time.Time,
) ([]models.UAStat, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT CASE WHEN v.ua = '' THEN 'unknown' ELSE v.ua END AS ua,
		       COUNT(*) AS count
		FROM links l
		JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = $1 AND l.alias = $2
		  AND v.time >= $3 AND v.time < $4
		GROUP BY 1 ORDER BY count DESC, ua`, host, alias, micros(start), micros(end))
	if err != nil {
		return nil, fmt.Errorf("failed to count ua: %w", err)
	}
	defer rows.Close()

	var rs []models.UAStat
	for rows.Next() {
		var r models.UAStat
		if err := rows.Scan(&r.UA, &r.Count); err != nil {
			return nil, fmt.Errorf("failed to fetch ua results: %w", err)
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to fetch ua results: %w", err)
	}
	return rs, nil
}

// StatVisitHist buckets by hour, over non-bot visits. The index listing
// still counts all traffic; StatBots reports the difference.
func (db *store) StatVisitHist(
	ctx context.Context,
	host, alias string,
	start, end time.Time,
) ([]models.TimeHist, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT date_trunc('hour', to_timestamp(v.time / 1000000.0)) AS bucket,
		       COUNT(*) AS pv,
		       COUNT(DISTINCT v.ip) AS uv`+nonBotVisits+`
		GROUP BY bucket ORDER BY bucket`, host, alias, micros(start), micros(end))
	if err != nil {
		return nil, fmt.Errorf("failed to count time hist: %w", err)
	}
	defer rows.Close()

	var rs []models.TimeHist
	for rows.Next() {
		var r models.TimeHist
		if err := rows.Scan(&r.Time, &r.PV, &r.UV); err != nil {
			return nil, fmt.Errorf("failed to fetch time hist results: %w", err)
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to fetch time hist results: %w", err)
	}
	return rs, nil
}

// StatVisit counts PV and UV for the given aliases.
//
// UV is a distinct count over ip, not over visitor_id. The cookie
// carrying visitor_id is a session cookie and RecordVisit mints one
// whenever a request arrives without it, so 277,318 of 348,356 recorded
// visits invented a visitor. It counts visits, not people.
func (db *store) StatVisit(
	ctx context.Context,
	host string,
	aliases []string,
) ([]models.VisitRecord, error) {
	if len(aliases) == 0 {
		return nil, nil
	}

	rows, err := db.pool.Query(ctx, `
		SELECT l.alias,
		       COUNT(v.id) AS pv,
		       COUNT(DISTINCT v.ip) AS uv
		FROM links l
		LEFT JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = $1 AND l.alias = ANY($2)
		GROUP BY l.alias
		ORDER BY pv DESC, uv DESC`, host, aliases)
	if err != nil {
		return nil, fmt.Errorf("failed to count visit: %w", err)
	}
	defer rows.Close()

	var rs []models.VisitRecord
	for rows.Next() {
		var r models.VisitRecord
		if err := rows.Scan(&r.Alias, &r.PV, &r.UV); err != nil {
			return nil, fmt.Errorf("failed to fetch visit results: %w", err)
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to fetch visit results: %w", err)
	}
	return rs, nil
}

// pgUnique reports which unique constraint failed.
//
// links_pkey and visits_pkey are PostgreSQL's names for the primary keys.
// links_host_alias_key is its name for UNIQUE (host, alias). Only the
// latter is ErrAliasExists. A primary-key collision is retried with a
// new id.
func pgUnique(err error) uniqueKind {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		return uniqueNone
	}
	switch pgErr.ConstraintName {
	case "links_host_alias_key":
		return uniqueAlias
	case "links_pkey", "visits_pkey":
		return uniqueID
	default:
		return uniqueNone
	}
}

// Timestamps are Unix microseconds, UTC. The column is an integer, so
// time.Time is not passed through to the driver.
func micros(t time.Time) int64 { return t.UTC().UnixMicro() }

func fromMicros(n int64) time.Time { return time.UnixMicro(n).UTC() }
