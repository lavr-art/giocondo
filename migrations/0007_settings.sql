-- Panel settings changed at runtime. A value is a JSON document owned by the service that reads it,
-- so adding a setting to a document needs no migration.
CREATE TABLE settings (
    key        TEXT      PRIMARY KEY,
    value      TEXT      NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
