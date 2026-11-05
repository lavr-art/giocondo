-- API tokens with scopes (monitor | node-sync | admin): only the hex sha256 of the token is stored.
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    name         TEXT      NOT NULL,
    scope        TEXT      NOT NULL,
    token_hash   TEXT      NOT NULL UNIQUE,
    created_at   TIMESTAMP NOT NULL,
    last_used_at TIMESTAMP
);

-- Background watcher state (admin.Service.Watch): the last seen status and
-- the warnings sent, so events go out once and survive restarts.
ALTER TABLE users ADD COLUMN last_status TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN warned_traffic BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN warned_expiry BOOLEAN NOT NULL DEFAULT FALSE;

-- runtime: remote | local (agent on the panel machine). desired_hash is the sha256 of the config
-- the panel built for the node; applied_hash is what runs on the node; when they differ the node is dirty.
ALTER TABLE nodes ADD COLUMN runtime TEXT NOT NULL DEFAULT 'remote';
ALTER TABLE nodes ADD COLUMN desired_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN applied_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN apply_error TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN down_notified BOOLEAN NOT NULL DEFAULT FALSE;
