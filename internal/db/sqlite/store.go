package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rukins/gore/internal/db/migrate"
	"github.com/rukins/gore/internal/models"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"uuid"
)

// store is the SQLite backend. One connection, a file on a local disk.
type store struct {
	db *sql.DB
}

func (db *store) Close() error { return db.db.Close() }

// idAttempts is how many times an insert retries a primary-key collision.
const idAttempts = 8

func (db *store) StoreAlias(ctx context.Context, r *models.Link) error {
	// valid_from is stored as given, including the zero value. short.go
	// treats a zero time as "valid since always".
	now := micros(time.Now().UTC())
	var err error
	for range idAttempts {
		id := migrate.NextID()
		r.ID = strconv.FormatInt(id, 10)
		_, err = db.db.ExecContext(ctx, `
			INSERT INTO links
				(id, host, alias, url, private, trust, valid_from,
				 created_by, updated_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.Host, r.Alias, r.URL, r.Private, r.Trust, micros(r.ValidFrom),
			r.CreatedBy, r.UpdatedBy, now, now,
		)
		if err == nil {
			return nil
		}
		switch sqliteUnique(err) {
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

	tag, err := db.db.ExecContext(ctx, `
		UPDATE links SET
			alias = ?, url = ?, private = ?, trust = ?,
			valid_from = ?, updated_by = ?, updated_at = ?
		WHERE id = ?`,
		r.Alias, r.URL, r.Private, r.Trust,
		micros(r.ValidFrom), r.UpdatedBy, micros(time.Now().UTC()), id,
	)
	if err != nil {
		if sqliteUnique(err) == uniqueAlias {
			return migrate.ErrAliasExists
		}
		return fmt.Errorf("failed to update alias %s: %v", r.Alias, err)
	}
	n, err := tag.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to update alias %s: %v", r.Alias, err)
	}
	if n == 0 {
		return fmt.Errorf("failed to update alias %s: no such link", r.Alias)
	}
	return nil
}

func (db *store) DeleteAlias(ctx context.Context, host, alias string) error {
	_, err := db.db.ExecContext(ctx,
		`DELETE FROM links WHERE host = ? AND alias = ?`, host, alias)
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
	err := db.db.QueryRowContext(ctx, `
		SELECT id, host, alias, url, private, trust, valid_from,
		       created_by, updated_by, created_at, updated_at
		FROM links WHERE host = ? AND alias = ?`, host, alias,
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

func (db *store) FetchAliasAll(
	ctx context.Context,
	host string,
	public bool,
	pageSize, pageNum int64,
) ([]models.LinkIndex, int64, error) {
	offset := (pageNum - 1) * pageSize

	var total int64
	countQuery := `SELECT COUNT(*) FROM links WHERE host = ?`
	if public {
		countQuery += ` AND NOT private`
	}
	if err := db.db.QueryRowContext(ctx, countQuery, host).Scan(&total); err != nil {
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
		WHERE l.host = ?
		GROUP BY l.id
		ORDER BY l.updated_at DESC
		LIMIT ? OFFSET ?`
	if public {
		// No join: the public index shows neither URL nor counts.
		query = `
		SELECT id, host, alias, '', private, trust,
		       valid_from, created_by, updated_by,
		       created_at, updated_at, 0, 0
		FROM links
		WHERE host = ? AND NOT private
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?`
	}

	rows, err := db.db.QueryContext(ctx, query, host, pageSize, offset)
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
		_, err = db.db.ExecContext(ctx, `
			INSERT INTO visits
				(id, host, alias, visitor_id, ip, ua, referer,
				 referer_host, browser, os, device, is_bot, time)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, v.Host, v.Alias, v.VisitorID, v.IP, v.UA, v.Referer,
			v.RefererHost, v.Browser, v.OS, v.Device, v.IsBot, micros(when))
		if err == nil {
			return v.VisitorID, nil
		}
		if sqliteUnique(err) == uniqueID {
			continue
		}
		return "", fmt.Errorf("failed to insert record: %w", err)
	}
	return "", fmt.Errorf("failed to insert record: %w", err)
}

// The same allow-list as the PostgreSQL store. The column name is
// concatenated into the query, so it comes from this map and never from
// the request.
var groupable = map[string]string{
	"referer": "Direct",
	"browser": "Unknown",
	"os":      "Unknown",
	"device":  "Unknown",
}

var column = map[string]string{
	"referer": "referer_host",
	"browser": "browser",
	"os":      "os",
	"device":  "device",
}

const nonBotVisits = `
	FROM links l
	JOIN visits v ON v.host = l.host AND v.alias = l.alias
	WHERE l.host = ? AND l.alias = ?
	  AND v.time >= ? AND v.time < ?
	  AND NOT v.is_bot`

func (db *store) StatGroup(
	ctx context.Context,
	host, alias, by string,
	start, end time.Time,
) ([]models.NameCount, error) {
	empty, ok := groupable[by]
	if !ok {
		return nil, fmt.Errorf("cannot group visits by %q", by)
	}

	rows, err := db.db.QueryContext(ctx, `
		SELECT CASE WHEN v.`+column[by]+` = '' THEN '`+empty+`'
		            ELSE v.`+column[by]+` END AS name,
		       COUNT(*) AS count`+nonBotVisits+`
		GROUP BY 1 ORDER BY count DESC, name`,
		host, alias, micros(start), micros(end))
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

func (db *store) StatBots(
	ctx context.Context,
	host, alias string,
	start, end time.Time,
) (models.BotStat, error) {
	var b models.BotStat
	err := db.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT v.ip)
		FROM links l
		JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = ? AND l.alias = ?
		  AND v.time >= ? AND v.time < ?
		  AND v.is_bot`, host, alias, micros(start), micros(end)).Scan(&b.PV, &b.UV)
	if err != nil {
		return models.BotStat{}, fmt.Errorf("failed to count bots: %w", err)
	}
	return b, nil
}

func (db *store) StatUA(
	ctx context.Context,
	host, alias string,
	start, end time.Time,
) ([]models.UAStat, error) {
	rows, err := db.db.QueryContext(ctx, `
		SELECT CASE WHEN v.ua = '' THEN 'unknown' ELSE v.ua END AS ua,
		       COUNT(*) AS count
		FROM links l
		JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = ? AND l.alias = ?
		  AND v.time >= ? AND v.time < ?
		GROUP BY 1 ORDER BY count DESC, ua`,
		host, alias, micros(start), micros(end))
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

// Hour buckets are UTC because the stored instant is Unix microseconds.
// strftime then prints the UTC hour, which is what date_trunc('hour', …)
// returns once the PostgreSQL session zone is pinned to UTC.
func (db *store) StatVisitHist(
	ctx context.Context,
	host, alias string,
	start, end time.Time,
) ([]models.TimeHist, error) {
	rows, err := db.db.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%dT%H:00:00Z', v.time / 1000000, 'unixepoch') AS bucket,
		       COUNT(*) AS pv,
		       COUNT(DISTINCT v.ip) AS uv`+nonBotVisits+`
		GROUP BY bucket ORDER BY bucket`,
		host, alias, micros(start), micros(end))
	if err != nil {
		return nil, fmt.Errorf("failed to count time hist: %w", err)
	}
	defer rows.Close()

	var rs []models.TimeHist
	for rows.Next() {
		var (
			r      models.TimeHist
			bucket string
		)
		if err := rows.Scan(&bucket, &r.PV, &r.UV); err != nil {
			return nil, fmt.Errorf("failed to fetch time hist results: %w", err)
		}
		t, err := time.Parse(time.RFC3339, bucket)
		if err != nil {
			return nil, fmt.Errorf("failed to parse time hist bucket %q: %w", bucket, err)
		}
		r.Time = t.UTC()
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to fetch time hist results: %w", err)
	}
	return rs, nil
}

func (db *store) StatVisit(
	ctx context.Context,
	host string,
	aliases []string,
) ([]models.VisitRecord, error) {
	if len(aliases) == 0 {
		return nil, nil
	}

	rows, err := db.db.QueryContext(ctx, `
		SELECT l.alias,
		       COUNT(v.id) AS pv,
		       COUNT(DISTINCT v.ip) AS uv
		FROM links l
		LEFT JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = ? AND l.alias IN (`+placeholders(len(aliases))+`)
		GROUP BY l.alias
		ORDER BY pv DESC, uv DESC`, visitArgs(host, aliases)...)
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

func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func visitArgs(host string, aliases []string) []any {
	args := make([]any, 0, 1+len(aliases))
	args = append(args, host)
	for _, a := range aliases {
		args = append(args, a)
	}
	return args
}

type uniqueKind int

const (
	uniqueNone uniqueKind = iota
	uniqueAlias
	uniqueID
)

// sqliteUnique reports which unique constraint failed.
//
// Extended result codes are enabled, so the code is SQLITE_CONSTRAINT_UNIQUE
// rather than the primary SQLITE_CONSTRAINT shared with NOT NULL and CHECK.
// The message names the columns: links.host, links.alias is ErrAliasExists,
// and links.id or visits.id is retried with a new id.
func sqliteUnique(err error) uniqueKind {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return uniqueNone
	}
	switch se.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
	default:
		return uniqueNone
	}
	msg := se.Error()
	switch {
	case strings.Contains(msg, "links.host"):
		return uniqueAlias
	case strings.Contains(msg, "links.id"), strings.Contains(msg, "visits.id"):
		return uniqueID
	default:
		return uniqueNone
	}
}
