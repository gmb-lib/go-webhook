-- The table shape a relational Store implementation uses. Written for Flyway naming, but
-- plain SQL: copy it into your own migration set and put it in your own schema (the
-- statements below use the schema name `webhook`; replace it). Column names follow the
-- Go types in the library one for one, so a Store built on these tables is a thin mapping.
--
-- Secrets are stored as REFERENCES into your secret store, never as values: the library
-- receives the bytes from your Store implementation at send time. Two references allow a
-- rotation with an overlap; the previous one is cleared when the rotation ends.

CREATE TABLE IF NOT EXISTS webhook.subscription (
    id                  text         PRIMARY KEY,
    client_id           text         NOT NULL,
    endpoint_url        text         NOT NULL,
    secret_ref_current  text         NOT NULL,
    secret_ref_previous text,
    previous_expires_at timestamptz,                    -- when the previous secret stops signing
    event_types         text[]       NOT NULL DEFAULT '{}',   -- empty = every type
    enabled             boolean      NOT NULL DEFAULT true,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    updated_at          timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_webhook_subscription_client ON webhook.subscription (client_id);

-- The event body is kept so a delivery can be re-sent byte-identical, and so a host can
-- show an integrator what it missed. Retention is the host's policy.
CREATE TABLE IF NOT EXISTS webhook.event (
    id           text         PRIMARY KEY,
    client_id    text         NOT NULL,
    event_type   text         NOT NULL,
    occurred_at  timestamptz  NOT NULL,
    payload      jsonb        NOT NULL,
    created_at   timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_webhook_event_client ON webhook.event (client_id, occurred_at DESC);

-- One row per event per matching subscription; the attempt record lives on the row.
-- Append-only in spirit: a row is updated by the worker, never deleted while the event
-- is retained.
CREATE TABLE IF NOT EXISTS webhook.delivery (
    id                text         PRIMARY KEY,
    event_id          text         NOT NULL REFERENCES webhook.event(id) ON DELETE CASCADE,
    subscription_id   text         NOT NULL REFERENCES webhook.subscription(id) ON DELETE CASCADE,
    status            text         NOT NULL DEFAULT 'pending',
    attempts          integer      NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz,
    last_http_status  integer,
    last_error        text,
    last_attempt_at   timestamptz,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT ck_webhook_delivery_status CHECK (status IN
      ('pending', 'retrying', 'delivered', 'dead-letter', 'dropped'))
);

-- What the worker polls: non-terminal rows whose next attempt is due, oldest first.
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_due
    ON webhook.delivery (next_attempt_at)
    WHERE status IN ('pending', 'retrying');

CREATE INDEX IF NOT EXISTS idx_webhook_delivery_event ON webhook.delivery (event_id);
