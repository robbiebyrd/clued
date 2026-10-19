-- Billing schema: accounts, invoices and the routines that keep them.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Everything billing owns lives in its own schema.
CREATE SCHEMA billing;

-- How a customer feels about their last invoice.
CREATE TYPE billing.mood AS ENUM ('sad', 'ok', 'happy');

-- An amount with its currency.
CREATE TYPE money_amount AS (amount numeric, currency char(3));

-- A syntactically valid e-mail address.
CREATE DOMAIN email_address AS text CHECK (length(VALUE) > 3);

CREATE SEQUENCE billing.invoice_number;

-- A team pays for its members.
CREATE TABLE billing.teams (
  id bigserial PRIMARY KEY,
  name text NOT NULL UNIQUE
);

-- The users of the service, one row per sign-up.
CREATE TABLE billing.users (
  id bigserial PRIMARY KEY,
  email email_address NOT NULL UNIQUE,
  team_id bigint REFERENCES billing.teams (id),
  mood billing.mood,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invoices (
  id bigserial PRIMARY KEY,
  user_id bigint NOT NULL,
  total money_amount,
  CONSTRAINT invoices_user_fk FOREIGN KEY (user_id) REFERENCES billing.users (id)
);

ALTER TABLE invoices ADD COLUMN paid_at timestamptz;

CREATE UNIQUE INDEX users_email_idx ON billing.users (lower(email));

-- Users with a team, for the admin pages.
CREATE VIEW billing.active_users AS
  SELECT u.id, u.email, t.name AS team
  FROM billing.users u
  JOIN billing.teams t ON t.id = u.team_id;

CREATE MATERIALIZED VIEW invoice_totals AS
  SELECT user_id, count(*) AS invoices FROM invoices GROUP BY user_id;

-- The sum of a user's invoices.
CREATE OR REPLACE FUNCTION billing.user_total(uid bigint) RETURNS numeric
LANGUAGE sql AS $$
  SELECT sum((total).amount) FROM invoices WHERE user_id = uid;
$$;

CREATE FUNCTION touch_user() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.created_at := now();
  RETURN NEW;
END;
$$;

-- Rebuilds the totals after a batch import.
CREATE PROCEDURE billing.refresh_totals() LANGUAGE sql AS $$
  REFRESH MATERIALIZED VIEW invoice_totals;
$$;

CREATE TRIGGER users_touch BEFORE INSERT ON billing.users
  FOR EACH ROW EXECUTE FUNCTION touch_user();
