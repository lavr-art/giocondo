-- Initial schema. Types and syntax are portable to Postgres except autoincrement
-- (INTEGER PRIMARY KEY in SQLite, BIGINT GENERATED ALWAYS AS IDENTITY in Postgres).
-- Time is stored as a UTC string "2006-01-02T15:04:05Z" (see dbTime), days as "2006-01-02".

CREATE TABLE users (
    id                  INTEGER PRIMARY KEY,
    name                TEXT      NOT NULL UNIQUE,
    token               TEXT      NOT NULL UNIQUE,
    uuid                TEXT      NOT NULL UNIQUE,
    traffic_limit_bytes BIGINT    NOT NULL DEFAULT 0, -- 0 = no limit
    expires_at          TIMESTAMP,                    -- NULL = no expiry
    enabled             BOOLEAN   NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMP NOT NULL,
    wg_private_key      TEXT,
    wg_public_key       TEXT,
    wg_preshared_key    TEXT,
    wg_address          TEXT UNIQUE                   -- 10.70.0.N/32
);

CREATE TABLE nodes (
    id         INTEGER PRIMARY KEY,
    name       TEXT      NOT NULL UNIQUE,
    host       TEXT      NOT NULL,              -- domain or IP for clients
    ip         TEXT      NOT NULL DEFAULT '',
    region     TEXT      NOT NULL DEFAULT '',
    status     TEXT      NOT NULL DEFAULT 'new', -- new | online | offline | disabled
    last_seen  TIMESTAMP,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE inbounds (
    id      INTEGER PRIMARY KEY,
    node_id BIGINT  NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    type    TEXT    NOT NULL,           -- vless-reality | hysteria2 | wireguard
    tag     TEXT    NOT NULL,
    port    INTEGER NOT NULL,
    params  TEXT    NOT NULL DEFAULT '{}', -- JSON with protocol parameters
    UNIQUE (node_id, tag)
);

CREATE TABLE traffic (
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id BIGINT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    day     DATE   NOT NULL,
    up      BIGINT NOT NULL DEFAULT 0,
    down    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, node_id, day)
);

CREATE INDEX traffic_day ON traffic (day);
CREATE INDEX inbounds_node ON inbounds (node_id);
