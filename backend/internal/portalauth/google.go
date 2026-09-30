package portalauth

import (
	"context"
	"fmt"

	"google.golang.org/api/idtoken"
)

// Exchange swaps an authorization code for a verified Identity.
func (s *Service) Exchange(ctx context.Context, code string) (Identity, error) {
	tok, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("portalauth: exchange code: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, fmt.Errorf("portalauth: token response missing id_token")
	}
	return s.Verify(ctx, raw)
}

// Verify validates a raw id_token against Google's keys and the configured
// audience, then applies email policy.
func (s *Service) Verify(ctx context.Context, rawIDToken string) (Identity, error) {
	payload, err := idtoken.Validate(ctx, rawIDToken, s.cfg.ClientID)
	if err != nil {
		return Identity{}, fmt.Errorf("portalauth: verify id_token: %w", err)
	}
	sub, _ := payload.Claims["sub"].(string)
	email, _ := payload.Claims["email"].(string)
	verified, _ := payload.Claims["email_verified"].(bool)
	if sub == "" || email == "" {
		return Identity{}, fmt.Errorf("portalauth: id_token missing sub/email")
	}
	return s.accept(Identity{Sub: sub, Email: email, EmailVerified: verified})
}
