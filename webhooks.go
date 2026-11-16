package sigli

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
)

type WebhooksService struct{ c *Client }

type Webhook struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	CreatedAt *string  `json:"created_at,omitempty"`
	Secret    string   `json:"secret,omitempty"`
}

func (s *WebhooksService) List(ctx context.Context) ([]Webhook, error) {
	var out []Webhook
	return out, s.c.do(ctx, http.MethodGet, "/webhooks", nil, nil, &out)
}

func (s *WebhooksService) Create(ctx context.Context, webhookURL string, events []string, opts *RequestOptions) (*Webhook, error) {
	out := &Webhook{}
	return out, s.c.do(ctx, http.MethodPost, "/webhooks", map[string]any{"url": webhookURL, "events": events}, opts, out)
}

func (s *WebhooksService) Delete(ctx context.Context, id string) error {
	_, err := s.c.Request(ctx, http.MethodDelete, "/webhooks/"+url.PathEscape(id), nil, nil)
	return err
}

const SignatureHeader = "X-Sigli-Signature"

const EventHeader = "X-Sigli-Event"

func VerifyWebhookSignature(payload []byte, header, secret string) bool {
	header = strings.TrimSpace(header)
	if header == "" || secret == "" {
		return false
	}
	if len(header) > 7 && strings.EqualFold(header[:7], "sha256=") {
		header = header[7:]
	}
	got, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(header)))
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hmac.Equal(got, mac.Sum(nil))
}

func (c *Client) VerifyWebhookSignature(payload []byte, header, secret string) bool {
	return VerifyWebhookSignature(payload, header, secret)
}
