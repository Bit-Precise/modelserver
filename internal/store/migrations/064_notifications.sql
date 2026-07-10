-- 064_notifications.sql
--
-- Notifications: platform-authored in-dashboard messages. Two tables:
--   1. notifications        — the message itself. Soft-deleted via
--                              deleted_at; audience shape enforced by
--                              CHECK constraint.
--   2. notification_reads   — per-user read state; row present = read;
--                              idempotent insert via ON CONFLICT.
--
-- See docs/superpowers/specs/2026-07-10-notifications-design.md.

CREATE TABLE notifications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title         TEXT NOT NULL,
    body          TEXT NOT NULL,
    audience_type TEXT NOT NULL,
    audience_id   UUID,
    created_by    UUID NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT audience_shape CHECK (
        (audience_type = 'global' AND audience_id IS NULL) OR
        (audience_type IN ('project','user') AND audience_id IS NOT NULL)
    )
);

CREATE INDEX idx_notifications_alive_created
    ON notifications (created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_notifications_project
    ON notifications (audience_id)
    WHERE audience_type = 'project' AND deleted_at IS NULL;

CREATE INDEX idx_notifications_user
    ON notifications (audience_id)
    WHERE audience_type = 'user' AND deleted_at IS NULL;

CREATE TABLE notification_reads (
    notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (notification_id, user_id)
);

CREATE INDEX idx_notification_reads_user ON notification_reads (user_id);
