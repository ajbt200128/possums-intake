-- Historical split-account DDL, copied from accepted main 9a8b6ba.
-- Refusal-only test fixture; never production migration input.

CREATE TABLE ledger_meta (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 schema_version INTEGER NOT NULL CHECK(schema_version=1),
 clock_high_water_ms INTEGER NOT NULL CHECK(typeof(clock_high_water_ms)='integer' AND clock_high_water_ms>=0 AND clock_high_water_ms<=9999999999999)
);
CREATE TABLE ledger_accounts (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=15 AND id NOT GLOB '*[^a-z0-9]*'),
 account_id TEXT NOT NULL CHECK(length(CAST(account_id AS BLOB)) BETWEEN 1 AND 128),
 balance TEXT NOT NULL CHECK(typeof(balance)='text' AND length(balance) BETWEEN 1 AND 20 AND balance NOT GLOB '*[^0-9]*' AND (balance='0' OR substr(balance,1,1) BETWEEN '1' AND '9') AND (length(balance)<20 OR balance<='18446744073709551615')),
 enabled INTEGER NOT NULL CHECK(typeof(enabled)='integer' AND enabled IN (0,1))
);
CREATE TABLE ledger_reservations (
 reservation_id TEXT PRIMARY KEY NOT NULL CHECK(length(reservation_id)=32 AND reservation_id NOT GLOB '*[^0-9a-f]*'),
 idempotency_key TEXT NOT NULL UNIQUE,
 account_id TEXT NOT NULL REFERENCES ledger_accounts(account_id) ON DELETE RESTRICT,
 maximum TEXT NOT NULL CHECK(typeof(maximum)='text' AND length(maximum) BETWEEN 1 AND 20 AND maximum NOT GLOB '*[^0-9]*' AND substr(maximum,1,1) BETWEEN '1' AND '9'),
 timeout_seconds INTEGER NOT NULL CHECK(typeof(timeout_seconds)='integer' AND timeout_seconds BETWEEN 1 AND 1800),
 expires_at INTEGER NOT NULL CHECK(typeof(expires_at)='integer' AND expires_at>0 AND expires_at<=9999999999999),
 state TEXT NOT NULL CHECK(state IN ('active','settled','refunded','expired')),
 charged_amount TEXT NULL CHECK(charged_amount IS NULL OR (typeof(charged_amount)='text' AND length(charged_amount) BETWEEN 1 AND 20 AND charged_amount NOT GLOB '*[^0-9]*' AND (charged_amount='0' OR substr(charged_amount,1,1) BETWEEN '1' AND '9'))),
 terminal_at INTEGER NULL CHECK(terminal_at IS NULL OR (typeof(terminal_at)='integer' AND terminal_at>0)),
 retain_until INTEGER NULL CHECK(retain_until IS NULL OR (typeof(retain_until)='integer' AND retain_until>terminal_at)),
 CHECK((state='active' AND charged_amount IS NULL AND terminal_at IS NULL AND retain_until IS NULL) OR (state!='active' AND charged_amount IS NOT NULL AND terminal_at IS NOT NULL AND retain_until IS NOT NULL)),
 CHECK(state NOT IN ('refunded','expired') OR charged_amount='0'),
 CHECK(state!='expired' OR terminal_at=expires_at),
 CHECK(state NOT IN ('settled','refunded') OR terminal_at<expires_at)
);
CREATE TABLE ledger_credits (
 idempotency_key TEXT PRIMARY KEY NOT NULL,
 account_id TEXT NOT NULL REFERENCES ledger_accounts(account_id) ON DELETE RESTRICT,
 amount TEXT NOT NULL CHECK(typeof(amount)='text' AND length(amount) BETWEEN 1 AND 20 AND amount NOT GLOB '*[^0-9]*' AND substr(amount,1,1) BETWEEN '1' AND '9'),
 balance_after TEXT NOT NULL CHECK(typeof(balance_after)='text' AND length(balance_after) BETWEEN 1 AND 20 AND balance_after NOT GLOB '*[^0-9]*' AND (balance_after='0' OR substr(balance_after,1,1) BETWEEN '1' AND '9')),
 committed_at INTEGER NOT NULL CHECK(typeof(committed_at)='integer' AND committed_at>0),
 retain_until INTEGER NOT NULL CHECK(typeof(retain_until)='integer' AND retain_until>committed_at)
);
CREATE UNIQUE INDEX ledger_accounts_account_id ON ledger_accounts (account_id);
CREATE INDEX ledger_reservations_account_active ON ledger_reservations(account_id,state,expires_at);
CREATE INDEX ledger_reservations_expiry ON ledger_reservations(state,expires_at);
CREATE INDEX ledger_reservations_prune ON ledger_reservations(retain_until) WHERE state!='active';
CREATE INDEX ledger_credits_prune ON ledger_credits(retain_until);
INSERT INTO ledger_meta(singleton,schema_version,clock_high_water_ms) VALUES(1,1,0);
