-- +goose Up
-- Admin-editable WhatsApp configuration. The singleton row starts empty: a
-- NULL (or empty) column means "use the env default", so a deploy changes
-- nothing until an admin saves a value at /admin/whatsapp. Secrets and
-- transport knobs (WA_API_KEY, WA_API_URL, WA_INSTANCE, WA_WEBHOOK_SECRET,
-- WA_SEND_RATE, WA_ENABLED) stay env-only and never reach this table.

CREATE TABLE IF NOT EXISTS wa_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    customer_enabled BOOLEAN,
    reminder_enabled BOOLEAN,
    remind_hour      SMALLINT,
    review_enabled   BOOLEAN,
    review_url       TEXT,
    group_mode       TEXT,
    staff_group_jid  TEXT,
    digest_enabled   BOOLEAN,
    digest_hour      SMALLINT,
    outbound_enabled BOOLEAN,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO wa_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- Per-template message copy override. A missing row means the built-in copy
-- in internal/whatsapp/render.go, so "reset to default" is a DELETE.
CREATE TABLE IF NOT EXISTS wa_template_overrides (
    template_key TEXT PRIMARY KEY,
    body TEXT NOT NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS wa_template_overrides;
DROP TABLE IF EXISTS wa_settings;
