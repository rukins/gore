package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/rukins/gore/internal/models"
)

// rederiveBatchSize is how many visits are read per round trip.
const rederiveBatchSize = 10000

// Rederive recomputes the derived columns of stored visits for one host.
func Rederive(ctx context.Context, uri, host string) error {
	db, _, err := openDB(ctx, uri)
	if err != nil {
		return err
	}
	defer db.Close()

	var lastID, seen, changed int64
	for {
		n, err := rederiveBatch(ctx, db, host, &lastID, &changed)
		if err != nil {
			return err
		}
		seen += n
		if n < rederiveBatchSize {
			break
		}
		log.Printf("re-derived %d visits, %d changed", seen, changed)
	}

	log.Printf("re-derived %d visits for host %v, %d changed", seen, host, changed)
	return nil
}

func rederiveBatch(
	ctx context.Context,
	db *sql.DB,
	host string,
	lastID, changed *int64,
) (int64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, ua, referer, browser, os, device, is_bot, referer_host
		FROM visits
		WHERE host = ? AND id > ?
		ORDER BY id
		LIMIT ?`, host, *lastID, rederiveBatchSize)
	if err != nil {
		return 0, fmt.Errorf("cannot read visits: %w", err)
	}

	type update struct {
		id int64
		v  models.Visit
	}
	var (
		seen  int64
		batch []update
	)
	for rows.Next() {
		var (
			id                               int64
			ua, referer                      string
			browser, os, device, refererHost string
			isBot                            bool
		)
		if err := rows.Scan(&id, &ua, &referer,
			&browser, &os, &device, &isBot, &refererHost); err != nil {
			rows.Close()
			return 0, fmt.Errorf("cannot scan visit: %w", err)
		}
		*lastID = id
		seen++

		v := models.Visit{UA: ua, Referer: referer}
		v.Derive()
		if v.Browser == browser && v.OS == os && v.Device == device &&
			v.IsBot == isBot && v.RefererHost == refererHost {
			continue
		}
		batch = append(batch, update{id: id, v: v})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("cannot read visits: %w", err)
	}

	for _, u := range batch {
		if _, err := db.ExecContext(ctx, `
			UPDATE visits SET browser = ?, os = ?, device = ?,
			                  is_bot = ?, referer_host = ?
			WHERE id = ?`,
			u.v.Browser, u.v.OS, u.v.Device,
			u.v.IsBot, u.v.RefererHost, u.id); err != nil {
			return 0, fmt.Errorf("cannot update visit %d: %w", u.id, err)
		}
	}
	*changed += int64(len(batch))
	return seen, nil
}
