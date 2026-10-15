package finance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var discordClient = &http.Client{Timeout: 10 * time.Second}

// PostDiscord sends a plain text message to a Discord webhook. A non-2xx
// response is an error.
func PostDiscord(ctx context.Context, url, content string) error {
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("finance: discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := discordClient.Do(req)
	if err != nil {
		return fmt.Errorf("finance: discord post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("finance: discord returned status %d", resp.StatusCode)
	}
	return nil
}
