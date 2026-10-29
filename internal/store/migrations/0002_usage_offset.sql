-- Reset a user's traffic counter (e.g. when a package is renewed) without deleting history:
-- used = sum of traffic - offset. ResetUsage writes the current sums here.
ALTER TABLE users ADD COLUMN usage_offset_up BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN usage_offset_down BIGINT NOT NULL DEFAULT 0;
