-- Statements run inside an explicit transaction are tagged with its ID and
-- the user's optional label, in history and in the audit log.
ALTER TABLE query_history ADD COLUMN tx_id TEXT NOT NULL DEFAULT '';
ALTER TABLE query_history ADD COLUMN tx_label TEXT NOT NULL DEFAULT '';
CREATE INDEX query_history_tx ON query_history (tx_id, id) WHERE tx_id <> '';

ALTER TABLE audit_log ADD COLUMN tx_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN tx_label TEXT NOT NULL DEFAULT '';
