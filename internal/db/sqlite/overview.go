package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/rukins/gore/internal/models"
)

func (db *store) StatOverview(
	ctx context.Context,
	host string,
	start, end time.Time,
) (models.Overview, error) {
	var o models.Overview

	// COUNT(CASE …) is the SQLite spelling of COUNT(*) FILTER (WHERE …).
	// The host is bound twice: each ? takes the next argument.
	err := db.db.QueryRowContext(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM links WHERE host = ?),
		  COUNT(v.id),
		  COUNT(CASE WHEN NOT v.is_bot THEN 1 END),
		  COUNT(CASE WHEN v.is_bot THEN 1 END)
		FROM links l
		JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = ? AND v.time >= ? AND v.time < ?`,
		host, host, micros(start), micros(end),
	).Scan(&o.Links, &o.Visits, &o.People, &o.Bots)
	if err != nil {
		return models.Overview{}, fmt.Errorf("failed to total visits: %w", err)
	}

	rows, err := db.db.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%d', v.time / 1000000, 'unixepoch') AS day,
		       COUNT(*) AS pv,
		       COUNT(DISTINCT v.ip) AS uv
		FROM links l
		JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = ? AND v.time >= ? AND v.time < ? AND NOT v.is_bot
		GROUP BY 1 ORDER BY 1`, host, micros(start), micros(end))
	if err != nil {
		return models.Overview{}, fmt.Errorf("failed to read the series: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d models.DayCount
		if err := rows.Scan(&d.Day, &d.PV, &d.UV); err != nil {
			return models.Overview{}, fmt.Errorf("failed to read the series: %w", err)
		}
		o.Series = append(o.Series, d)
	}
	if err := rows.Err(); err != nil {
		return models.Overview{}, fmt.Errorf("failed to read the series: %w", err)
	}
	return o, nil
}

func (db *store) StatDaily(
	ctx context.Context,
	host string,
	aliases []string,
	start, end time.Time,
) (map[string][]models.DayCount, error) {
	if len(aliases) == 0 {
		return map[string][]models.DayCount{}, nil
	}

	args := make([]any, 0, 1+len(aliases)+2)
	args = append(args, host)
	for _, a := range aliases {
		args = append(args, a)
	}
	args = append(args, micros(start), micros(end))

	rows, err := db.db.QueryContext(ctx, `
		SELECT l.alias,
		       strftime('%Y-%m-%d', v.time / 1000000, 'unixepoch') AS day,
		       COUNT(*) AS pv,
		       COUNT(DISTINCT v.ip) AS uv
		FROM links l
		JOIN visits v ON v.host = l.host AND v.alias = l.alias
		WHERE l.host = ? AND l.alias IN (`+placeholders(len(aliases))+`)
		  AND v.time >= ? AND v.time < ? AND NOT v.is_bot
		GROUP BY l.alias, 2
		ORDER BY l.alias, 2`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to read daily counts: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]models.DayCount, len(aliases))
	for rows.Next() {
		var (
			alias string
			d     models.DayCount
		)
		if err := rows.Scan(&alias, &d.Day, &d.PV, &d.UV); err != nil {
			return nil, fmt.Errorf("failed to read daily counts: %w", err)
		}
		out[alias] = append(out[alias], d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read daily counts: %w", err)
	}
	return out, nil
}
