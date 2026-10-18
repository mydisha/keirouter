-- Coupons for the 9router hosting product. A coupon either cuts the price
-- (kind 'discount': percent or fixed rupiah) or adds days to the paid period
-- (kind 'bonus_days'). max_uses caps redemptions (0 = unlimited).
--
-- used_count and held_count are the race-safe quota: checkout takes a slot
-- with a guarded UPDATE (used_count + held_count < max_uses) while its
-- payment is pending; settlement moves the slot from held to used, and an
-- expired or failed payment gives it back.
CREATE TABLE IF NOT EXISTS hosting_coupons (
  id              TEXT PRIMARY KEY,
  code            TEXT NOT NULL,
  kind            TEXT NOT NULL,
  discount_type   TEXT NOT NULL DEFAULT '',
  discount_value  BIGINT NOT NULL DEFAULT 0,
  bonus_days      INTEGER NOT NULL DEFAULT 0,
  max_uses        INTEGER NOT NULL DEFAULT 0,
  used_count      INTEGER NOT NULL DEFAULT 0,
  held_count      INTEGER NOT NULL DEFAULT 0,
  starts_at       TEXT NOT NULL DEFAULT '',
  expires_at      TEXT NOT NULL DEFAULT '',
  active          INTEGER NOT NULL DEFAULT 1,
  note            TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_hosting_coupons_code ON hosting_coupons(code);

-- One row per order that used a coupon. status: reserved (payment pending),
-- used (paid), released (payment failed or expired). The partial unique
-- index enforces one live use per coupon per API key.
CREATE TABLE IF NOT EXISTS hosting_coupon_redemptions (
  id              TEXT PRIMARY KEY,
  coupon_id       TEXT NOT NULL,
  key_id          TEXT NOT NULL,
  order_id        TEXT NOT NULL,
  status          TEXT NOT NULL,
  reserved_until  TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_hosting_coupon_redemptions_order ON hosting_coupon_redemptions(order_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_hosting_coupon_redemptions_live
  ON hosting_coupon_redemptions(coupon_id, key_id) WHERE status IN ('reserved', 'used');
CREATE INDEX IF NOT EXISTS idx_hosting_coupon_redemptions_due
  ON hosting_coupon_redemptions(status, reserved_until);

-- The order records what the coupon did, so settlement never re-reads a
-- coupon the admin may have edited since checkout.
ALTER TABLE payment_orders ADD COLUMN coupon_id TEXT NOT NULL DEFAULT '';
ALTER TABLE payment_orders ADD COLUMN coupon_code TEXT NOT NULL DEFAULT '';
ALTER TABLE payment_orders ADD COLUMN base_amount_idr BIGINT NOT NULL DEFAULT 0;
ALTER TABLE payment_orders ADD COLUMN bonus_days INTEGER NOT NULL DEFAULT 0;
