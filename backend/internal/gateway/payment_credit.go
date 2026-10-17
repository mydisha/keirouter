package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// ErrKeyDisabled is returned when a payment order's key is disabled, so callers
// can distinguish a fail-closed conflict from a real internal fault.
var ErrKeyDisabled = errors.New("payment: key is disabled")

// creditPaymentOrder applies an order's stored credit to its key exactly once.
// It is the single guarded path used by both the webhook and admin manual
// approval: it conditionally transitions the order away from pending and
// requires that transition to win, then atomically raises the key's budget and
// writes a key_topups ledger row in the same transaction. If the transition
// does not apply (order already handled) no credit is given.
//
// Fail closed: a missing or disabled key returns an error without crediting.
func (s *Server) creditPaymentOrder(ctx context.Context, order store.PaymentOrder, to store.PaymentOrderStatus, actor, reason string) (store.PaymentOrder, store.Budget, bool, error) {
	if order.Status != store.PaymentPending {
		return order, store.Budget{}, false, nil
	}
	if order.Product == store.ProductHosting {
		return store.PaymentOrder{}, store.Budget{}, false, fmt.Errorf("payment: order %s is a hosting order, not credit", order.ID)
	}
	if order.CreditMicros <= 0 {
		return store.PaymentOrder{}, store.Budget{}, false, fmt.Errorf("payment: order %s has non-positive credit %d", order.ID, order.CreditMicros)
	}

	key, err := s.identity.Get(ctx, order.KeyID)
	if err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, fmt.Errorf("payment: load key: %w", err)
	}
	if key.Disabled {
		return store.PaymentOrder{}, store.Budget{}, false, ErrKeyDisabled
	}

	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.budgets.LockKeyTopup(ctx, tx, key.ID); err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, err
	}

	// Resolve or create the key's api_key budget (total period = prepaid).
	budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, key.ID)
	if err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, err
	}
	var budget store.Budget
	if len(budgets) == 0 {
		now := time.Now()
		budget = store.Budget{
			ID: uuid.NewString(), TenantID: adminTenant, ScopeKind: store.ScopeAPIKey,
			ScopeID: key.ID, LimitMicros: 0, Period: "total", AlertPct: 80,
			HardCutoff: true, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.budgets.CreateOnTx(ctx, tx, budget); err != nil {
			return store.PaymentOrder{}, store.Budget{}, false, err
		}
	} else {
		budget = budgets[0]
	}

	// Purchased credit must live on a non-resetting "total" budget. An
	// existing periodic budget would re-grant the increment every period, so
	// convert it within this transaction (LockKeyTopup is held).
	if budget.Period != "total" {
		if err := s.budgets.SetPeriodOnTx(ctx, tx, budget.ID, "total"); err != nil {
			return store.PaymentOrder{}, store.Budget{}, false, err
		}
		budget.Period = "total"
	}

	paidAt := time.Now().UTC().Format(time.RFC3339)
	ok, err := s.db.PaymentOrders().TransitionOnTx(ctx, tx, order.ID, store.PaymentPending, to, actor, reason, paidAt)
	if err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, err
	}
	if !ok {
		return order, store.Budget{}, false, nil
	}

	before, after, err := s.budgets.IncrementLimitOnTx(ctx, tx, budget.ID, order.CreditMicros)
	if err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, err
	}
	budget.LimitMicros = after

	rec := store.KeyTopup{
		ID: uuid.NewString(), TenantID: adminTenant, KeyID: key.ID, BudgetID: budget.ID,
		AmountMicros: order.CreditMicros, Reason: nonEmpty(order.Reason, "payment gateway top-up"),
		LimitBeforeMicros: before, LimitAfterMicros: after,
		IdempotencyKey: "payment_order:" + order.ID, Actor: actor, CreatedAt: time.Now(),
	}
	if err := s.db.Topups().CreateOnTx(ctx, tx, rec); err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return store.PaymentOrder{}, store.Budget{}, false, err
	}
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCacheForScope(store.ScopeAPIKey, key.ID)
	}
	order.Status = to
	return order, budget, true, nil
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
