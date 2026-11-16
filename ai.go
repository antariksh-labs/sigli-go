package sigli

import (
	"context"
	"net/http"
)

type AIService struct{ c *Client }

func (s *AIService) call(ctx context.Context, op string, file FileInput, fields map[string]any, opts *RequestOptions) (map[string]any, error) {
	body := merge(fields, nil)
	if err := applyFile(body, file); err != nil {
		return nil, err
	}
	var out map[string]any
	return out, s.c.do(ctx, http.MethodPost, "/v1/extract/"+op, body, opts, &out)
}

func (s *AIService) Summary(ctx context.Context, file FileInput, instructions string, opts *RequestOptions) (map[string]any, error) {
	f := map[string]any{}
	if instructions != "" {
		f["instructions"] = instructions
	}
	return s.call(ctx, "summary", file, f, opts)
}

func (s *AIService) Structured(ctx context.Context, file FileInput, fields []string, opts *RequestOptions) (map[string]any, error) {
	f := map[string]any{}
	if len(fields) > 0 {
		f["fields"] = fields
	}
	return s.call(ctx, "structured", file, f, opts)
}

func (s *AIService) Translate(ctx context.Context, file FileInput, targetLanguage string, opts *RequestOptions) (map[string]any, error) {
	return s.call(ctx, "translate", file, map[string]any{"target_language": targetLanguage}, opts)
}

func (s *AIService) Query(ctx context.Context, file FileInput, query string, opts *RequestOptions) (map[string]any, error) {
	return s.call(ctx, "query", file, map[string]any{"query": query}, opts)
}
