package sigli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type Result struct {
	StatusCode  int
	ContentType string
	RequestID   string

	Bytes []byte

	Path string

	Data json.RawMessage
}

type ProcessResult = Result

func (r *Result) IsBinary() bool { return r.Data == nil }

func (r *Result) DecodeData(v any) error {
	if r.Data == nil {
		return argError("result is binary, not JSON")
	}
	return json.Unmarshal(r.Data, v)
}

func toResult(resp *Response) (*Result, error) {
	r := &Result{StatusCode: resp.StatusCode, ContentType: resp.ContentType, RequestID: resp.RequestID, Path: resp.Path}
	if resp.Path != "" {
		return r, nil
	}
	if resp.IsJSON() {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if len(bytes.TrimSpace(resp.Body)) > 0 {
			if err := json.Unmarshal(resp.Body, &env); err != nil {
				return nil, &Error{Status: resp.StatusCode, Code: "invalid_response", Message: err.Error(), RequestID: resp.RequestID, Body: resp.Body}
			}
		}
		if env.Data == nil {
			env.Data = json.RawMessage("null")
		}
		r.Data = env.Data
		return r, nil
	}
	r.Bytes = resp.Body
	return r, nil
}

func (c *Client) Process(ctx context.Context, convertType string, params map[string]any, opts *RequestOptions) (*ProcessResult, error) {
	if convertType == "" {
		return nil, argError("convertType is required")
	}
	body, err := withParams(params)
	if err != nil {
		return nil, err
	}
	o := mergeHeaders(opts, map[string]string{"X-Convert-Type": convertType})
	resp, err := c.Request(ctx, http.MethodPost, "/v1/process", body, o)
	if err != nil {
		return nil, err
	}
	return toResult(resp)
}

func (c *Client) HTMLToPDF(ctx context.Context, html string, params map[string]any, opts *RequestOptions) (*Result, error) {
	return c.Process(ctx, "html-to-pdf", merge(params, map[string]any{"source": html}), opts)
}

func (c *Client) URLToPDF(ctx context.Context, url string, params map[string]any, opts *RequestOptions) (*Result, error) {
	return c.Process(ctx, "url-to-pdf", merge(params, map[string]any{"source_url": url}), opts)
}

func (c *Client) Screenshot(ctx context.Context, target string, params map[string]any, opts *RequestOptions) (*Result, error) {
	key := "source"
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		key = "source_url"
	}
	return c.Process(ctx, "screenshot", merge(params, map[string]any{key: target}), opts)
}

func (c *Client) OCR(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (map[string]any, error) {
	return c.processJSON(ctx, "ocr", file, params, opts)
}

func (c *Client) PDFToMarkdown(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (map[string]any, error) {
	return c.processJSON(ctx, "pdf-to-markdown", file, params, opts)
}

func (c *Client) processJSON(ctx context.Context, typ string, file FileInput, params map[string]any, opts *RequestOptions) (map[string]any, error) {
	r, err := c.Process(ctx, typ, merge(params, map[string]any{"file": file}), opts)
	if err != nil {
		return nil, err
	}
	if r.IsBinary() {
		return nil, &Error{Status: r.StatusCode, Code: "invalid_response", Message: "expected JSON, got " + r.ContentType, RequestID: r.RequestID}
	}
	var out map[string]any
	if err := r.DecodeData(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func merge(params, extra map[string]any) map[string]any {
	out := make(map[string]any, len(params)+len(extra))
	for k, v := range params {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func mergeHeaders(opts *RequestOptions, h map[string]string) *RequestOptions {
	o := RequestOptions{}
	if opts != nil {
		o = *opts
	}
	hdr := make(map[string]string, len(o.Headers)+len(h))
	for k, v := range o.Headers {
		hdr[k] = v
	}
	for k, v := range h {
		hdr[k] = v
	}
	o.Headers = hdr
	return &o
}
