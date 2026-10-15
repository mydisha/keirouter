package finance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var discordClient = &http.Client{Timeout: 10 * time.Second}

// PostDiscord sends a plain text message to a Discord webhook. A non-2xx
// response is an error. Returned errors never contain the webhook URL, whose
// path carries the secret token.
func PostDiscord(ctx context.Context, webhookURL, content string) error {
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return redactURLError("finance: discord request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := discordClient.Do(req)
	if err != nil {
		return redactURLError("finance: discord post", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("finance: discord returned status %d", resp.StatusCode)
	}
	return nil
}

// redactURLError drops the request URL that *url.Error embeds in its message.
func redactURLError(prefix string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %s: %w", prefix, ue.Op, ue.Err)
	}
	return fmt.Errorf("%s failed", prefix)
}
