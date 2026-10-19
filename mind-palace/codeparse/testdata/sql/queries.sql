-- Reports run by the nightly job.
\i schema.sql
\ir 'helpers/dates.sql'

INSERT INTO billing.users (email, team_id) VALUES ('ada@example.com', 1);

INSERT INTO invoices (user_id, total) VALUES (1, ROW(12.50, 'EUR')::money_amount);

UPDATE billing.users SET mood = 'happy' WHERE id = 1;

UPDATE invoices SET paid_at = now() FROM payments WHERE payments.invoice_id = invoices.id;

DELETE FROM sessions WHERE expires_at < now();

CALL send_reminders(7);

SELECT u.email, billing.user_total(u.id) AS total, format_money(i.total)
FROM billing.active_users u
LEFT JOIN invoices i ON i.user_id = u.id
WHERE u.team_id = 1
ORDER BY total DESC
LIMIT 10;

WITH recent AS (
  SELECT * FROM invoices WHERE paid_at > now() - interval '7 days'
)
SELECT count(*) FROM recent JOIN billing.teams t ON true;

SELECT 'x'::billing.mood, COUNT(*) FROM audit.events;

CALL billing.refresh_totals();
