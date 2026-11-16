package sigli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const MaxBatchItems = 500

type BatchService struct{ c *Client }

type BatchSubmitOptions struct {
	ConvertType string

	Mixed bool

	WebhookURL     string
	IdempotencyKey string
	Timeout        time.Duration
}

type BatchSubmission struct {
	BatchID                   string `json:"batch_id"`
	BatchMode                 string `json:"batch_mode"`
	ConvertType               string `json:"convert_type"`
	TotalItems                int    `json:"total_items"`
	TotalCreditsEstimated     int64  `json:"total_credits_estimated"`
	TotalInvocationsEstimated int64  `json:"total_invocations_estimated"`
	Status                    string `json:"status"`
	PollURL                   string `json:"poll_url"`
}

type BatchResult struct {
	Index  int            `json:"index"`
	Status string         `json:"status"`
	Detail map[string]any `json:"detail,omitempty"`
	Error  any            `json:"error,omitempty"`
}

type Batch struct {
	BatchID                   string        `json:"batch_id"`
	BatchMode                 string        `json:"batch_mode"`
	ConvertType               string        `json:"convert_type"`
	JobType                   string        `json:"job_type"`
	Status                    string        `json:"status"`
	TotalItems                int64         `json:"total_items"`
	ProcessedItems            int64         `json:"processed_items"`
	FailedItems               int64         `json:"failed_items"`
	TotalCreditsEstimated     *int64        `json:"total_credits_estimated"`
	TotalInvocationsEstimated *int64        `json:"total_invocations_estimated"`
	WebhookURL                *string       `json:"webhook_url"`
	CreatedAt                 *string       `json:"created_at"`
	UpdatedAt                 *string       `json:"updated_at"`
	CompletedAt               *string       `json:"completed_at"`
	Results                   []BatchResult `json:"results,omitempty"`
}

func (b *Batch) IsTerminal() bool {
	switch b.Status {
	case "completed", "completed_with_errors", "failed":
		return true
	}
	return false
}

func (s *BatchService) Submit(ctx context.Context, items []map[string]any, o *BatchSubmitOptions) (*BatchSubmission, error) {
	if len(items) == 0 || len(items) > MaxBatchItems {
		return nil, argError(fmt.Sprintf("a batch needs 1..%d items (got %d)", MaxBatchItems, len(items)))
	}
	if o == nil {
		o = &BatchSubmitOptions{}
	}
	norm := make([]map[string]any, len(items))
	for i, it := range items {
		n, err := withParams(it)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		norm[i] = n
	}
	body := map[string]any{"items": norm}
	if o.WebhookURL != "" {
		body["webhook_url"] = o.WebhookURL
	}
	h := map[string]string{}
	if o.ConvertType != "" {
		h["X-Convert-Type"] = o.ConvertType
	}
	if o.Mixed {
		h["X-Batch-Mode"] = "mixed"
	}
	ro := &RequestOptions{IdempotencyKey: o.IdempotencyKey, Timeout: o.Timeout, Headers: h}
	out := &BatchSubmission{}
	return out, s.c.do(ctx, http.MethodPost, "/v1/batch/submit", body, ro, out)
}

func (s *BatchService) Get(ctx context.Context, batchID string) (*Batch, error) {
	out := &Batch{}
	return out, s.c.do(ctx, http.MethodGet, "/v1/batch/"+url.PathEscape(batchID), nil, nil, out)
}

func (s *BatchService) List(ctx context.Context) ([]Batch, error) {
	var out []Batch
	return out, s.c.do(ctx, http.MethodGet, "/v1/batch", nil, nil, &out)
}

type WaitOptions struct {
	PollInterval time.Duration
	MaxInterval  time.Duration
	Timeout      time.Duration
}

func (s *BatchService) Wait(ctx context.Context, batchID string, o *WaitOptions) (*Batch, error) {
	w := WaitOptions{PollInterval: time.Second, MaxInterval: 10 * time.Second, Timeout: 10 * time.Minute}
	if o != nil {
		if o.PollInterval > 0 {
			w.PollInterval = o.PollInterval
		}
		if o.MaxInterval > 0 {
			w.MaxInterval = o.MaxInterval
		}
		if o.Timeout > 0 {
			w.Timeout = o.Timeout
		}
	}
	deadline := time.Now().Add(w.Timeout)
	interval := w.PollInterval
	for {
		b, err := s.Get(ctx, batchID)
		if err != nil {
			return nil, err
		}
		if b.IsTerminal() {
			return b, nil
		}
		if time.Now().Add(interval).After(deadline) {
			return b, &Error{Code: "batch_timeout", Message: fmt.Sprintf("batch %s still %q after %s", batchID, b.Status, w.Timeout)}
		}
		if err := s.c.sleep(ctx, interval); err != nil {
			return nil, err
		}
		interval = time.Duration(float64(interval) * 1.5)
		if interval > w.MaxInterval {
			interval = w.MaxInterval
		}
	}
}

func (c *Client) WaitForBatch(ctx context.Context, batchID string, o *WaitOptions) (*Batch, error) {
	return c.Batch.Wait(ctx, batchID, o)
}
