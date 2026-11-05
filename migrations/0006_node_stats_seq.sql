-- The last traffic batch recorded per node. An agent resends an unacknowledged batch with the same
-- instance and seq, also after a panel restart, and the batch must be counted once.
CREATE TABLE node_stats_seq (
 node_id BIGINT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
 instance_id TEXT NOT NULL,
 seq BIGINT NOT NULL
);
