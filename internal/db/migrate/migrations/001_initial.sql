-- One migration for both engines. Both run this file as it is.
--
-- id is BIGINT and is supplied by the application. Neither engine
-- generates it. INTEGER PRIMARY KEY would be the row itself on SQLite,
-- but that spelling is 32-bit on PostgreSQL.
--
-- Timestamps are Unix microseconds, UTC, also supplied by the
-- application. BIGINT rather than INTEGER: on PostgreSQL INTEGER is
-- 32-bit and cannot hold a microsecond timestamp. There is no
-- DEFAULT NOW().
--
-- visitor_id is text and stays nullable. Historical rows have no
-- identifier, and this migration does not invent one.
--
-- links holds one row per short alias. The identity of a link is the pair
-- (host, alias): one process serves several hosts, and the same alias may
-- mean different things on each.
CREATE TABLE links (
    id         BIGINT PRIMARY KEY,
    host       TEXT NOT NULL,
    alias      TEXT NOT NULL,
    url        TEXT NOT NULL,
    private    BOOLEAN NOT NULL DEFAULT FALSE,
    trust      BOOLEAN NOT NULL DEFAULT FALSE,
    valid_from BIGINT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    UNIQUE (host, alias)
);

-- visits holds one row per redirect served.
--
-- There is deliberately no foreign key from alias to links. A visit is
-- recorded before the alias is resolved, so 404s are counted; the index
-- page records a visit with an empty alias; and deleting a link keeps its
-- history. On the production data that is 21% of the rows, which a foreign
-- key would reject. Queries that must match a link therefore join links
-- explicitly rather than relying on the schema to guarantee it.
CREATE TABLE visits (
    id           BIGINT PRIMARY KEY,
    host         TEXT NOT NULL,
    alias        TEXT NOT NULL,
    -- Null when the value recorded before this column was validated was
    -- not parseable. Historical rows are not given invented identifiers.
    visitor_id   TEXT,
    -- Text, not inet: with gdpr.hide_ip the address is replaced by a
    -- SHA-1 hex digest, or by the literal "unknown" when hashing fails.
    ip           TEXT NOT NULL,
    ua           TEXT NOT NULL DEFAULT '',
    referer      TEXT NOT NULL DEFAULT '',
    -- Derived once at write time from ua and referer.
    referer_host TEXT NOT NULL DEFAULT '',
    browser      TEXT NOT NULL DEFAULT '',
    os           TEXT NOT NULL DEFAULT '',
    device       TEXT NOT NULL DEFAULT '',
    is_bot       BOOLEAN NOT NULL DEFAULT FALSE,
    time         BIGINT NOT NULL
);

-- Time-ranged stats for one alias.
CREATE INDEX idx_visits_host_alias_time ON visits (host, alias, time DESC);
-- PV/UV for one alias: uv is a distinct count over ip.
CREATE INDEX idx_visits_host_alias_ip ON visits (host, alias, ip);
-- The index listing orders by updated_at within a host.
CREATE INDEX idx_links_host_updated ON links (host, updated_at DESC);
