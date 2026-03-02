-- Migration: 002_add_webhook_status_skipped
-- Description: Add 'skipped' to webhook_status CHECK constraint for events with no callback URL

ALTER TABLE monitor_events DROP CONSTRAINT monitor_events_webhook_status_check;
ALTER TABLE monitor_events ADD CONSTRAINT monitor_events_webhook_status_check
    CHECK (webhook_status IN ('pending', 'sent', 'failed', 'skipped'));
