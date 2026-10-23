package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a thin REST client for one Evolution API instance. It is safe for
// concurrent use (http.Client is) and never logs the API key.
type Client struct {
	BaseURL  string
	APIKey   string
	Instance string
	HTTP     *http.Client
}

func NewClient(baseURL, apiKey, instance string) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		APIKey:   apiKey,
		Instance: instance,
		HTTP:     &http.Client{Timeout: 20 * time.Second},
	}
}

// APIError is a non-2xx response from Evolution. 4xx (except 429) means the
// request can never succeed and must not be retried (e.g. invalid number).
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	body := e.Body
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return fmt.Sprintf("evolution api %d: %s", e.Status, body)
}

// Permanent reports whether retrying is pointless.
func (e *APIError) Permanent() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusTooManyRequests
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("apikey", c.APIKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return data, nil
}

type sendTextResponse struct {
	Key struct {
		ID string `json:"id"`
	} `json:"key"`
	Status string `json:"status"`
}

// SendText delivers a plain text message and returns the WhatsApp message id
// (empty when Evolution does not echo one).
func (c *Client) SendText(ctx context.Context, phone, text string) (string, error) {
	body := map[string]any{
		"number":      phone,
		"text":        text,
		"delay":       0,
		"linkPreview": true,
	}
	data, err := c.do(ctx, http.MethodPost, "/message/sendText/"+url.PathEscape(c.Instance), body)
	if err != nil {
		return "", err
	}
	var out sendTextResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode sendText: %w", err)
	}
	return out.Key.ID, nil
}

// ConnectionState returns "open" when the instance is paired and online.
func (c *Client) ConnectionState(ctx context.Context) (string, error) {
	data, err := c.do(ctx, http.MethodGet, "/instance/connectionState/"+url.PathEscape(c.Instance), nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Instance struct {
			State string `json:"state"`
		} `json:"instance"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode connectionState: %w", err)
	}
	return out.Instance.State, nil
}

// SetWebhook registers the instance webhook (phase 2 uses this with the
// shared-secret header; the global env webhook stays off until then).
func (c *Client) SetWebhook(ctx context.Context, webhookURL string, events []string) error {
	body := map[string]any{
		"webhook": map[string]any{
			"enabled":  true,
			"url":      webhookURL,
			"byEvents": false,
			"base64":   false,
			"events":   events,
		},
	}
	_, err := c.do(ctx, http.MethodPost, "/webhook/set/"+url.PathEscape(c.Instance), body)
	return err
}
