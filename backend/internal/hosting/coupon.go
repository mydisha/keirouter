package hosting

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// MaxBonusDays caps a bonus-days coupon.
const MaxBonusDays = 365

var couponCodeRe = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{2,31}$`)

// NormalizeCouponCode upper-cases and trims a coupon code. Codes are matched
// case-insensitively.
func NormalizeCouponCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// ValidateCoupon normalizes an admin-entered coupon and rejects bad values.
func ValidateCoupon(c *store.HostingCoupon) error {
	c.Code = NormalizeCouponCode(c.Code)
	if !couponCodeRe.MatchString(c.Code) {
		return errors.New("code must be 3-32 characters: letters, digits, - or _")
	}
	c.Note = strings.TrimSpace(c.Note)
	if len(c.Note) > 500 {
		return errors.New("note is too long")
	}
	if c.MaxUses < 0 {
		return errors.New("max_uses must be 0 (unlimited) or more")
	}
	if !c.ExpiresAt.IsZero() && !c.StartsAt.IsZero() && !c.ExpiresAt.After(c.StartsAt) {
		return errors.New("expires_at must be after starts_at")
	}
	switch c.Kind {
	case store.CouponKindDiscount:
		c.BonusDays = 0
		switch c.DiscountType {
		case store.CouponDiscountPercent:
			if c.DiscountValue < 1 || c.DiscountValue > 100 {
				return errors.New("percent discount must be 1-100")
			}
		case store.CouponDiscountFixed:
			if c.DiscountValue < 1 {
				return errors.New("fixed discount must be at least 1 rupiah")
			}
		default:
			return errors.New("discount_type must be percent or fixed")
		}
	case store.CouponKindBonusDays:
		c.DiscountType, c.DiscountValue = "", 0
		if c.BonusDays < 1 || c.BonusDays > MaxBonusDays {
			return fmt.Errorf("bonus_days must be 1-%d", MaxBonusDays)
		}
	case store.CouponKindFree:
		c.DiscountType, c.DiscountValue, c.BonusDays = "", 0, 0
	default:
		return errors.New("kind must be discount, bonus_days or free")
	}
	return nil
}

// Coupon rejection codes shown to the portal.
const (
	CouponNotFound    = "coupon_not_found"
	CouponInactive    = "coupon_inactive"
	CouponNotStarted  = "coupon_not_started"
	CouponExpired     = "coupon_expired"
	CouponExhausted   = "coupon_exhausted"
	CouponAlreadyUsed = "coupon_already_used"
	CouponNoDiscount  = "coupon_no_discount"
)

// CouponMessage is the customer-facing text for a rejection code.
func CouponMessage(code string) string {
	switch code {
	case CouponNotFound:
		return "Kode kupon tidak ditemukan."
	case CouponInactive:
		return "Kupon ini sudah tidak aktif."
	case CouponNotStarted:
		return "Kupon ini belum bisa dipakai."
	case CouponExpired:
		return "Kupon ini sudah kedaluwarsa."
	case CouponExhausted:
		return "Kuota kupon ini sudah habis."
	case CouponAlreadyUsed:
		return "Anda sudah memakai kupon ini."
	case CouponNoDiscount:
		return "Kupon ini tidak memberi potongan karena harga sudah di batas minimum pembayaran."
	}
	return "Kupon tidak bisa dipakai."
}

// CouponUsable checks the parts of a coupon that do not depend on the
// caller: active flag, validity window and remaining uses. It returns a
// rejection code, or "" when usable.
func CouponUsable(c store.HostingCoupon, now time.Time) string {
	switch {
	case !c.Active:
		return CouponInactive
	case !c.StartsAt.IsZero() && now.Before(c.StartsAt):
		return CouponNotStarted
	case !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt):
		return CouponExpired
	case c.MaxUses > 0 && c.UsedCount+c.HeldCount >= c.MaxUses:
		return CouponExhausted
	}
	return ""
}

// Quote is the price of one hosting period after a coupon.
type Quote struct {
	BaseIDR     int64
	DiscountIDR int64
	FinalIDR    int64
	BonusDays   int
}

// NoDiscount reports a discount coupon that takes nothing off, because the
// price is already at the minimum charge.
func (q Quote) NoDiscount(c store.HostingCoupon) bool {
	return c.Kind == store.CouponKindDiscount && q.DiscountIDR == 0
}

// QuoteCoupon applies a coupon to the base price. A discount never drops
// the price below minIDR, because the payment gateway cannot charge less;
// a free coupon skips the gateway, so it prices the period at 0.
func QuoteCoupon(base int64, c store.HostingCoupon, minIDR int64) Quote {
	q := Quote{BaseIDR: base, FinalIDR: base}
	switch c.Kind {
	case store.CouponKindDiscount:
		var off int64
		if c.DiscountType == store.CouponDiscountPercent {
			off = base * c.DiscountValue / 100
		} else {
			off = c.DiscountValue
		}
		final := base - off
		if final < minIDR {
			final = minIDR
		}
		if final > base {
			final = base
		}
		q.FinalIDR = final
		q.DiscountIDR = base - final
	case store.CouponKindBonusDays:
		q.BonusDays = c.BonusDays
	case store.CouponKindFree:
		q.FinalIDR, q.DiscountIDR = 0, base
	}
	return q
}
