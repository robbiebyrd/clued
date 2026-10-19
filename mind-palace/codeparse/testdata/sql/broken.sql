-- A migration cut off half-way: the table's column list never closes.
CREATE TABLE accounts (
  id bigserial PRIMARY KEY,
  owner text NOT NULL,

CREATE TABLE ledger (
  id bigserial PRIMARY KEY,
  account_id bigint REFERENCES accounts (id)
);

-- Still found after the error.
CREATE VIEW balances AS SELECT account_id, count(*) FROM ledger GROUP BY account_id;

SELECT * FROM ledger WHERE;
