package hosting

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestQuoteCoupon_HostingMinimum(t *testing.T) {
	half := store.HostingCoupon{Kind: store.CouponKindDiscount, DiscountType: store.CouponDiscountPercent, DiscountValue: 50}
	cases := []struct {
		name             string
		base, final, off int64
		noDiscount       bool
	}{
		{"half of 10.000 is 5.000", 10000, 5000, 5000, false},
		{"held at the 5.000 minimum", 8000, 5000, 3000, false},
		{"already at the minimum", 5000, 5000, 0, true},
	}
	for _, tc := range cases {
		q := QuoteCoupon(tc.base, half, 5000)
		if q.FinalIDR != tc.final || q.DiscountIDR != tc.off || q.NoDiscount(half) != tc.noDiscount {
			t.Errorf("%s: got final %d off %d noDiscount %v", tc.name, q.FinalIDR, q.DiscountIDR, q.NoDiscount(half))
		}
	}
	bonus := store.HostingCoupon{Kind: store.CouponKindBonusDays, BonusDays: 7}
	if QuoteCoupon(5000, bonus, 5000).NoDiscount(bonus) {
		t.Error("a bonus-days coupon is never a no-discount coupon")
	}
}

func TestQuoteCoupon_Free(t *testing.T) {
	free := store.HostingCoupon{Kind: store.CouponKindFree}
	q := QuoteCoupon(10000, free, 5000)
	if q.FinalIDR != 0 || q.DiscountIDR != 10000 || q.NoDiscount(free) {
		t.Errorf("free coupon: got %+v", q)
	}
	c := store.HostingCoupon{Code: "GRATIS", Kind: store.CouponKindFree, DiscountType: "percent", DiscountValue: 9, BonusDays: 3}
	if err := ValidateCoupon(&c); err != nil || c.DiscountValue != 0 || c.BonusDays != 0 || c.DiscountType != "" {
		t.Errorf("validate free: err %v, coupon %+v", err, c)
	}
}
