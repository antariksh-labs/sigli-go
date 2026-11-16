package sigli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const Version = "0.1.1"

const DefaultBaseURL = "https://api.sigli.dev"

const (
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 2
	maxBackoff        = 8 * time.Second
	maxRetryAfter     = 60 * time.Second
	maxErrorBodyBytes = 1 << 20
)

type Client struct {
	apiKey     string
	baseURL    string
	timeout    time.Duration
	maxRetries int
	httpClient *http.Client

	sleep func(ctx context.Context, d time.Duration) error

	PDF       *PDFService
	AI        *AIService
	Batch     *BatchService
	Workflows *WorkflowsService
	Webhooks  *WebhooksService
	Audit     *AuditService
}

type Option func(*Client)

func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n < 0 {
			n = 0
		}
		c.maxRetries = n
	}
}

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

func NewClient(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("SIGLI_API_KEY")
	}
	c := &Client{
		apiKey:     apiKey,
		baseURL:    os.Getenv("SIGLI_BASE_URL"),
		timeout:    defaultTimeout,
		maxRetries: defaultMaxRetries,
		httpClient: &http.Client{},
		sleep:      sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	if c.apiKey == "" {
		return nil, &Error{Code: "missing_api_key", Message: "an API key is required (pass it or set SIGLI_API_KEY)"}
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	c.baseURL = strings.TrimRight(c.baseURL, "/")
	c.PDF = &PDFService{c: c}
	c.AI = &AIService{c: c}
	c.Batch = &BatchService{c: c}
	c.Workflows = &WorkflowsService{c: c, Triggers: &WorkflowTriggersService{c: c}}
	c.Webhooks = &WebhooksService{c: c}
	c.Audit = &AuditService{c: c}
	return c, nil
}

type RequestOptions struct {
	IdempotencyKey string

	OutputPath string

	Timeout time.Duration

	Headers map[string]string
}

type Response struct {
	StatusCode  int
	Header      http.Header
	ContentType string
	RequestID   string

	Body []byte

	Path string
}

func (r *Response) IsJSON() bool { return strings.Contains(r.ContentType, "application/json") }

func (c *Client) Request(ctx context.Context, method, path string, body any, opts *RequestOptions) (*Response, error) {
	if opts == nil {
		opts = &RequestOptions{}
	}
	method = strings.ToUpper(method)
	if opts.IdempotencyKey != "" || hasHeader(opts.Headers, "Idempotency-Key") {
		k := strings.TrimSpace(opts.IdempotencyKey)
		if opts.IdempotencyKey == "" {
			k = strings.TrimSpace(getHeader(opts.Headers, "Idempotency-Key"))
		}
		if k == "" || len(k) > 128 {
			return nil, &Error{Code: "invalid_idempotency_key", Message: "idempotency key must be 1..128 characters"}
		}
	}

	var payload []byte
	contentType := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		payload = b
		contentType = "application/octet-stream"
	default:
		var err error
		payload, err = json.Marshal(b)
		if err != nil {
			return nil, &Error{Code: "invalid_argument", Message: "cannot encode request body: " + err.Error()}
		}
		contentType = "application/json"
	}

	idempotent := opts.IdempotencyKey != "" || hasHeader(opts.Headers, "Idempotency-Key")
	for attempt := 0; ; attempt++ {
		resp, err := c.doOnce(ctx, method, path, payload, contentType, opts)
		if err == nil {
			return resp, nil
		}
		var se *Error
		if !errors.As(err, &se) || attempt >= c.maxRetries || !shouldRetry(se, method, idempotent) {
			return nil, err
		}
		delay := backoff(attempt)
		if se.RetryAfter > 0 {
			delay = se.RetryAfter
		}
		if serr := c.sleep(ctx, delay); serr != nil {
			return nil, serr
		}
	}
}

func (c *Client) doOnce(ctx context.Context, method, path string, payload []byte, contentType string, opts *RequestOptions) (*Response, error) {
	timeout := c.timeout
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	url := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		url = c.baseURL + path
	}
	req, err := http.NewRequestWithContext(actx, method, url, rdr)
	if err != nil {
		return nil, &Error{Code: "invalid_argument", Message: err.Error()}
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("User-Agent", "sigli-go/"+Version)
	req.Header.Set("Accept", "application/json, */*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if opts.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", strings.TrimSpace(opts.IdempotencyKey))
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportError(ctx, actx, err)
	}
	defer res.Body.Close()

	out := &Response{
		StatusCode:  res.StatusCode,
		Header:      res.Header,
		ContentType: res.Header.Get("Content-Type"),
		RequestID:   res.Header.Get("X-Request-Id"),
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBodyBytes))
		return nil, newHTTPError(res, raw)
	}

	if opts.OutputPath != "" && !out.IsJSON() {
		if err := streamToFile(res.Body, opts.OutputPath); err != nil {
			return nil, transportError(ctx, actx, err)
		}
		out.Path = opts.OutputPath
		return out, nil
	}
	out.Body, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, transportError(ctx, actx, err)
	}
	return out, nil
}

func streamToFile(r io.Reader, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err = f.Close(); err != nil {
		os.Remove(path)
	}
	return err
}

func transportError(parent, attemptCtx context.Context, err error) error {

	if parent.Err() != nil {
		return parent.Err()
	}
	if attemptCtx.Err() == context.DeadlineExceeded {
		return &Error{Code: "timeout", Message: "request timed out", cause: err}
	}
	return &Error{Code: "network_error", Message: err.Error(), cause: err}
}

func shouldRetry(e *Error, method string, idempotent bool) bool {
	switch method {
	case http.MethodPost, http.MethodPatch:
		if !idempotent {
			return false
		}
	}
	switch e.Status {
	case 0:
		return e.Code == "network_error" || e.Code == "timeout"
	case 429:

		return e.Code != "quota_exceeded"
	case 503:

		return e.Code != "feature_unavailable"
	case 502, 504:
		return true
	}
	return false
}

func backoff(attempt int) time.Duration {
	base := 500 * time.Millisecond << uint(attempt)
	if base > maxBackoff || base <= 0 {
		base = maxBackoff
	}
	half := base / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	}
	if d < 0 {
		d = 0
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func hasHeader(h map[string]string, name string) bool { return getHeader(h, name) != "" }

func getHeader(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path string, body any, opts *RequestOptions, out any) error {
	resp, err := c.Request(ctx, method, path, body, opts)
	if err != nil {
		return err
	}
	return decodeData(resp, out)
}

func decodeData(resp *Response, out any) error {
	if out == nil || len(bytes.TrimSpace(resp.Body)) == 0 {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return &Error{Status: resp.StatusCode, Code: "invalid_response", Message: "cannot decode response: " + err.Error(), RequestID: resp.RequestID, Body: resp.Body}
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return &Error{Status: resp.StatusCode, Code: "invalid_response", Message: fmt.Sprintf("cannot decode data: %v", err), RequestID: resp.RequestID, Body: resp.Body}
	}
	return nil
}
