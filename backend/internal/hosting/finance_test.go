package hosting

import (
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestCostInMonth(t *testing.T) {
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, WIB)
	monthly := store.HostingCost{AmountIDR: 100, Period: store.CostMonthly, StartDate: "2026-09-15"}
	require.EqualValues(t, 100, CostInMonth(monthly, oct))
	require.EqualValues(t, 0, CostInMonth(monthly, oct.AddDate(0, -2, 0)), "before start")

	ended := monthly
	ended.EndDate = "2026-09-30"
	require.EqualValues(t, 0, CostInMonth(ended, oct), "ended last month")
	ended.EndDate = "2026-10-01"
	require.EqualValues(t, 100, CostInMonth(ended, oct), "ends inside the month")

	once := store.HostingCost{AmountIDR: 900, Period: store.CostOneTime, StartDate: "2026-10-31"}
	require.EqualValues(t, 900, CostInMonth(once, oct))
	require.EqualValues(t, 0, CostInMonth(once, oct.AddDate(0, 1, 0)))
}

func TestMonths(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, WIB)
	orders := []store.PaymentOrder{
		{Product: store.ProductHosting, Status: store.PaymentCompleted, AmountIDR: 50000, PaidAt: "2026-10-02T03:00:00Z"},
		// 2026-09-30T18:00Z is 1 Oct 01:00 WIB: counts in October.
		{Product: store.ProductHosting, Status: store.PaymentManual, AmountIDR: 50000, PaidAt: "2026-09-30T18:00:00Z"},
		{Product: store.ProductHosting, Status: store.PaymentCompleted, AmountIDR: 50000, PaidAt: "2026-09-10T00:00:00Z"},
		{Product: store.ProductHosting, Status: store.PaymentCompleted, AmountIDR: 50000, PaidAt: "2026-10-05T00:00:00Z", Reason: "refund_required: instance was deleted"},
		{Product: store.ProductHosting, Status: store.PaymentPending, AmountIDR: 50000},
		{Product: store.ProductCredit, Status: store.PaymentCompleted, AmountIDR: 999999, PaidAt: "2026-10-03T00:00:00Z"},
	}
	costs := []store.HostingCost{{AmountIDR: 120000, Period: store.CostMonthly, StartDate: "2026-09-01"}}
	m := Months(orders, costs, now, 2)
	require.Len(t, m, 2)
	require.Equal(t, "2026-09", m[0].Month)
	require.EqualValues(t, 50000, m[0].RevenueIDR)
	require.Equal(t, "2026-10", m[1].Month)
	require.EqualValues(t, 100000, m[1].RevenueIDR)
	require.Equal(t, 2, m[1].Orders)
	require.EqualValues(t, 50000, m[1].RefundIDR)
	require.EqualValues(t, 120000, m[1].CostIDR)
	require.EqualValues(t, -20000, m[1].ProfitIDR)
}
