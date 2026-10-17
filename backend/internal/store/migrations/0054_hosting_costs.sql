-- Operating costs of the 9router hosting product (VPS rent, domains, one-off
-- purchases) for the admin profit view. Amounts are whole rupiah. A monthly
-- cost counts in every month from start_date through end_date (open-ended
-- when end_date is empty); a one_time cost counts in the month of start_date.
CREATE TABLE IF NOT EXISTS hosting_costs (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  category    TEXT NOT NULL DEFAULT 'vps',
  amount_idr  BIGINT NOT NULL,
  period      TEXT NOT NULL DEFAULT 'monthly',
  start_date  TEXT NOT NULL,
  end_date    TEXT NOT NULL DEFAULT '',
  note        TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
