-- Host overrides: addresses the client receives in the subscription instead of the node host (fallback IPs and
-- domains, relays, CDN). If an inbound has at least one record, only these go into the subscription.
CREATE TABLE hosts (
    id         INTEGER PRIMARY KEY,
    inbound_id BIGINT  NOT NULL REFERENCES inbounds (id) ON DELETE CASCADE,
    address    TEXT    NOT NULL,           -- domain or IP
    port       INTEGER NOT NULL DEFAULT 0, -- 0 = inbound port
    sni        TEXT    NOT NULL DEFAULT '', -- empty = protocol default
    path       TEXT    NOT NULL DEFAULT '', -- for ws/grpc transports (not used yet)
    sort       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX hosts_inbound ON hosts (inbound_id);
