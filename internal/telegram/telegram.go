package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"webchecker/internal/config"
)

type Client struct {
	enabled bool
	token   string
	chatID  string
	http    *http.Client
}

func New(cfg config.Config) *Client {
	return &Client{
		enabled: cfg.TelegramEnabled && cfg.TelegramConfigured(),
		token:   cfg.TelegramBotToken,
		chatID:  cfg.TelegramChatID,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c.enabled
}

func (c *Client) Configured() bool {
	return c.token != "" && c.chatID != ""
}

func (c *Client) Send(ctx context.Context, text string) error {
	if !c.Configured() {
		return fmt.Errorf("telegram is not configured")
	}
	return c.send(ctx, text)
}

func (c *Client) Notify(ctx context.Context, text string) error {
	if !c.enabled {
		return nil
	}
	return c.send(ctx, text)
}

func (c *Client) send(ctx context.Context, text string) error {
	payload, err := json.Marshal(map[string]any{
		"chat_id":                  c.chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && !parsed.OK {
		if parsed.Description == "" {
			parsed.Description = string(body)
		}
		return fmt.Errorf("telegram api: %s", parsed.Description)
	}
	return nil
}
