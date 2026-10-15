package finance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// AlertDeps are the collaborators of one alert pass; tests substitute fakes.
type AlertDeps struct {
	Load     func(ctx context.Context) (AlertSettings, error)
	Balances func(ctx context.Context) ([]AccountBalance, error)
	SetFired func(ctx context.Context, accountID string, t time.Time) error
	// Post defaults to PostDiscord.
	Post func(ctx context.Context, url string, content string) error
	Now  func() time.Time
}

// AlertMessage renders the Discord text for a low account.
func AlertMessage(b AccountBalance) string {
	threshold := float64(b.Account.LowBalanceMicros) / microsPerUSD
	if b.BalanceUSD <= 0 {
		return fmt.Sprintf("Saldo %s sudah habis ($%.2f, batas $%.2f).", b.Account.Name, b.BalanceUSD, threshold)
	}
	msg := fmt.Sprintf("Saldo %s tinggal $%.2f (batas $%.2f).", b.Account.Name, b.BalanceUSD, threshold)
	if b.DaysLeft != nil {
		msg += fmt.Sprintf(" Perkiraan habis dalam %d hari.", max(0, int64(math.Floor(*b.DaysLeft))))
	}
	return msg
}

// CheckAlerts runs one pass. A low, armed account gets one message and is
// marked fired only after the post succeeds; a recovered, fired account is
// re-armed. Planned/archived accounts and accounts without a threshold are
// ignored. Errors never contain the webhook URL.
func CheckAlerts(ctx context.Context, d AlertDeps) error {
	s, err := d.Load(ctx)
	if err != nil {
		return err
	}
	if !s.Enabled || s.DiscordWebhookURL == "" {
		return nil
	}
	bals, err := d.Balances(ctx)
	if err != nil {
		return err
	}
	post := d.Post
	if post == nil {
		post = PostDiscord
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	var errs []error
	for _, b := range bals {
		a := b.Account
		if a.Status != "active" || a.LowBalanceMicros <= 0 {
			continue
		}
		switch {
		case b.Low && a.AlertFiredAt.IsZero():
			if err := post(ctx, s.DiscordWebhookURL, AlertMessage(b)); err != nil {
				errs = append(errs, fmt.Errorf("alert %s: %w", a.Name, err))
				continue
			}
			if err := d.SetFired(ctx, a.ID, now()); err != nil {
				errs = append(errs, err)
			}
		case !b.Low && !a.AlertFiredAt.IsZero():
			if err := d.SetFired(ctx, a.ID, time.Time{}); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
