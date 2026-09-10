-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    full_name TEXT NOT NULL,
    phone TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL CHECK (role IN ('customer', 'admin')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE locations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    address TEXT NOT NULL DEFAULT '',
    city TEXT NOT NULL DEFAULT '',
    timezone TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    duration_min INT NOT NULL CHECK (duration_min > 0),
    price_idr BIGINT NOT NULL CHECK (price_idr >= 0),
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX services_location_id_idx ON services(location_id);

CREATE TABLE staff (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    bio TEXT NOT NULL DEFAULT '',
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX staff_location_id_idx ON staff(location_id);

CREATE TABLE staff_hours (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id UUID NOT NULL REFERENCES staff(id) ON DELETE CASCADE,
    weekday INT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,
    CHECK (end_time > start_time)
);
CREATE INDEX staff_hours_staff_id_idx ON staff_hours(staff_id);

CREATE TABLE staff_services (
    staff_id UUID NOT NULL REFERENCES staff(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    PRIMARY KEY (staff_id, service_id)
);

CREATE TABLE bookings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
    service_id UUID NOT NULL REFERENCES services(id) ON DELETE RESTRICT,
    staff_id UUID NOT NULL REFERENCES staff(id) ON DELETE RESTRICT,
    start_at TIMESTAMPTZ NOT NULL,
    end_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending_payment', 'confirmed', 'cancelled', 'expired')),
    price_idr BIGINT NOT NULL CHECK (price_idr >= 0),
    hold_expires_at TIMESTAMPTZ,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_at > start_at)
);

-- Active bookings must not overlap for the same staff member.
CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER TABLE bookings
    ADD CONSTRAINT bookings_staff_no_overlap
    EXCLUDE USING gist (
        staff_id WITH =,
        tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (status IN ('pending_payment', 'confirmed'));

CREATE INDEX bookings_customer_id_idx ON bookings(customer_id);
CREATE INDEX bookings_staff_start_idx ON bookings(staff_id, start_at);
CREATE INDEX bookings_status_idx ON bookings(status);

CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL UNIQUE REFERENCES bookings(id) ON DELETE CASCADE,
    order_id TEXT NOT NULL UNIQUE,
    amount_idr BIGINT NOT NULL CHECK (amount_idr >= 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'failed', 'expired')),
    qr_string TEXT NOT NULL DEFAULT '',
    midtrans_trans_id TEXT NOT NULL DEFAULT '',
    raw_notification TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at TIMESTAMPTZ
);
CREATE INDEX payments_status_idx ON payments(status);

-- +goose Down
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS staff_services;
DROP TABLE IF EXISTS staff_hours;
DROP TABLE IF EXISTS staff;
DROP TABLE IF EXISTS services;
DROP TABLE IF EXISTS locations;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
