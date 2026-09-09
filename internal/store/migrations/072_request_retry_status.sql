-- Classifies request retry history independently from the final HTTP outcome.
-- Existing rows are normal because retry history was not recorded before this
-- migration.
ALTER TABLE requests
    ADD COLUMN IF NOT EXISTS retry_status TEXT NOT NULL DEFAULT 'normal';

CREATE INDEX IF NOT EXISTS idx_requests_retry_status
    ON requests (retry_status)
    WHERE retry_status <> 'normal';
