package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"webchecker/internal/config"
)

const maxMessageLen = 4000

type Client struct {
	enabled bool
	token   string
	chatID  string
	http    *http.Client
	poll    *http.Client
}

func New(cfg config.Config) *Client {
	return &Client{
		enabled: cfg.TelegramEnabled && cfg.TelegramConfigured(),
		token:   cfg.TelegramBotToken,
		chatID:  cfg.TelegramChatID,
		http:    &http.Client{Timeout: 10 * time.Second},
		poll:    &http.Client{Timeout: 40 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c.enabled
}

func (c *Client) Configured() bool {
	return c.token != "" && c.chatID != ""
}

func (c *Client) ChatID() string {
	return c.chatID
}

func (c *Client) Send(ctx context.Context, text string) error {
	if !c.Configured() {
		return fmt.Errorf("telegram is not configured")
	}
	return c.send(ctx, text)
}

func (c *Client) Notify(ctx context.Context, text string) error {
	if !c.Configured() {
		return nil
	}
	if !c.enabled {
		return nil
	}
	return c.send(ctx, text)
}

func (c *Client) send(ctx context.Context, text string) error {
	for _, part := range splitMessage(text, maxMessageLen) {
		if err := c.sendOnce(ctx, part); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) sendOnce(ctx context.Context, text string) error {
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

func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.apiPost(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false})
}

type chatMessage struct {
	Text string `json:"text"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

type update struct {
	UpdateID      int64        `json:"update_id"`
	Message       *chatMessage `json:"message"`
	EditedMessage *chatMessage `json:"edited_message"`
	ChannelPost   *chatMessage `json:"channel_post"`
}

func (u update) textAndChat() (string, int64, bool) {
	msg := u.Message
	if msg == nil {
		msg = u.EditedMessage
	}
	if msg == nil {
		msg = u.ChannelPost
	}
	if msg == nil {
		return "", 0, false
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return "", 0, false
	}
	return text, msg.Chat.ID, true
}

func (c *Client) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	payload, err := json.Marshal(map[string]any{
		"offset":          offset,
		"timeout":         25,
		"allowed_updates": []string{"message", "edited_message", "channel_post"},
	})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.poll.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram getUpdates status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var parsed struct {
		OK          bool     `json:"ok"`
		Description string   `json:"description"`
		Result      []update `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("telegram getUpdates decode: %w", err)
	}
	if !parsed.OK {
		if parsed.Description == "" {
			parsed.Description = string(body)
		}
		return nil, fmt.Errorf("telegram getUpdates: %s", parsed.Description)
	}
	return parsed.Result, nil
}

func (c *Client) apiPost(ctx context.Context, method string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram %s status %d", method, resp.StatusCode)
	}
	return nil
}

func splitMessage(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= limit {
		return []string{text}
	}
	var parts []string
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if b.Len() > 0 && b.Len()+1+len(line) > limit {
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
		}
		if len(line) > limit {
			if b.Len() > 0 {
				parts = append(parts, strings.TrimSpace(b.String()))
				b.Reset()
			}
			for len(line) > limit {
				parts = append(parts, line[:limit])
				line = line[limit:]
			}
			if line != "" {
				b.WriteString(line)
			}
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	if leftover := strings.TrimSpace(b.String()); leftover != "" {
		parts = append(parts, leftover)
	}
	return parts
}
