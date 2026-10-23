-- +goose Up
-- WhatsApp (Evolution API) plumbing: outbound outbox plus conversation and
-- message store. Outbound messages are queued in wa_outbox and drained by a
-- background worker (never sent inline from an HTTP handler); the dedupe key
-- makes Midtrans webhook replays and overlapping tickers harmless.
--
-- Conversations/messages are populated from phase 2 (inbound webhook + portal
-- inbox) but created here so the schema lands in one migration.

CREATE TABLE wa_conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone TEXT NOT NULL UNIQUE,
    customer_id UUID REFERENCES users(id) ON DELETE SET NULL,
    display_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'pending', 'closed')),
    assigned_to UUID REFERENCES users(id) ON DELETE SET NULL,
    unread_count INT NOT NULL DEFAULT 0,
    last_message_at TIMESTAMPTZ,
    last_message_preview TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX wa_conversations_activity_idx ON wa_conversations(last_message_at DESC NULLS LAST);

CREATE TABLE wa_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES wa_conversations(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    wa_message_id TEXT UNIQUE,
    body TEXT NOT NULL DEFAULT '',
    media_type TEXT,
    status TEXT NOT NULL DEFAULT 'received'
        CHECK (status IN ('queued', 'sent', 'delivered', 'read', 'failed', 'received')),
    error TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'customer'
        CHECK (source IN ('customer', 'staff', 'automation', 'system')),
    template_key TEXT NOT NULL DEFAULT '',
    sent_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ
);
CREATE INDEX wa_messages_conversation_idx ON wa_messages(conversation_id, created_at DESC);
CREATE INDEX wa_messages_wa_id_idx ON wa_messages(wa_message_id);

CREATE TABLE wa_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone TEXT NOT NULL,
    template_key TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    booking_id UUID REFERENCES bookings(id) ON DELETE CASCADE,
    dedupe_key TEXT UNIQUE,
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'cancelled')),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX wa_outbox_due_idx ON wa_outbox(scheduled_at) WHERE status = 'pending';
CREATE INDEX wa_outbox_booking_idx ON wa_outbox(booking_id);

-- Marketing consent only (transactional messages are covered by the booking).
-- Customers are `users` rows with role='customer'; staff rows simply never
-- get marketing sends. wa_opted_out_at blocks every non-transactional send.
ALTER TABLE users ADD COLUMN wa_marketing_opt_in BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN wa_opted_out_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE users DROP COLUMN wa_opted_out_at;
ALTER TABLE users DROP COLUMN wa_marketing_opt_in;
DROP TABLE wa_outbox;
DROP TABLE wa_messages;
DROP TABLE wa_conversations;
