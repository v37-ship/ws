-- +goose Up
-- Rename the legacy local admin login to the domain identity.
-- Idempotent: renames only when the new address is not already taken.
-- Preserves id, password_hash, and sessions (sessions key on user_id).

UPDATE users SET email = 'admin@wangga.biz.id', updated_at = now()
WHERE lower(email) = 'admin@spa.local'
  AND NOT EXISTS (SELECT 1 FROM users WHERE lower(email) = 'admin@wangga.biz.id');

-- +goose Down
UPDATE users SET email = 'admin@spa.local', updated_at = now()
WHERE lower(email) = 'admin@wangga.biz.id'
  AND NOT EXISTS (SELECT 1 FROM users WHERE lower(email) = 'admin@spa.local');
