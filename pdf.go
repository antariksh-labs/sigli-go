package sigli

import (
	"context"
	"net/http"
)

type PDFService struct{ c *Client }

type RedactRegion struct {
	Page   int     `json:"page"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type FormField struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type FormsExtractResult struct {
	Fields []FormField `json:"fields"`
	Count  int         `json:"count"`
}

func (s *PDFService) call(ctx context.Context, op string, file FileInput, fields, params map[string]any, opts *RequestOptions) (*Response, error) {
	body := merge(params, fields)
	if file != nil {
		if err := applyFile(body, file); err != nil {
			return nil, err
		}
	} else if op != "merge" {
		return nil, argError("a file input is required")
	}
	return s.c.Request(ctx, http.MethodPost, "/v1/pdf/"+op, body, opts)
}

func (s *PDFService) binary(ctx context.Context, op string, file FileInput, fields, params map[string]any, opts *RequestOptions) (*Result, error) {
	resp, err := s.call(ctx, op, file, fields, params, opts)
	if err != nil {
		return nil, err
	}
	return toResult(resp)
}

func (s *PDFService) Merge(ctx context.Context, files []FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	if len(files) < 2 {
		return nil, argError("merge requires at least 2 files")
	}
	var inline, urls []string
	for _, f := range files {
		if f == nil {
			return nil, argError("merge: nil file input")
		}
		b64, u, err := f.resolve()
		if err != nil {
			return nil, err
		}
		if u != "" {
			urls = append(urls, u)
			continue
		}
		if len(urls) > 0 {
			return nil, argError("merge: the API merges inline files before URL files, so a URL input cannot precede a bytes/path input; reorder or download the URL first")
		}
		inline = append(inline, b64)
	}
	fields := map[string]any{}
	if len(inline) > 0 {
		fields["files"] = inline
	}
	if len(urls) > 0 {
		fields["file_urls"] = urls
	}
	return s.binary(ctx, "merge", nil, fields, params, opts)
}

func (s *PDFService) Split(ctx context.Context, file FileInput, pages string, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "split", file, map[string]any{"pages": pages}, params, opts)
}

func (s *PDFService) Compress(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "compress", file, nil, params, opts)
}

func (s *PDFService) Rotate(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "rotate", file, nil, params, opts)
}

func (s *PDFService) Watermark(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	fields := map[string]any{}
	p := merge(params, nil)
	if img, ok := p["image"]; ok {
		delete(p, "image")
		fi, isFile := img.(FileInput)
		if !isFile {
			return nil, argError(`watermark "image" must be a sigli.FileInput`)
		}
		b64, u, err := fi.resolve()
		if err != nil {
			return nil, err
		}
		if u != "" {
			return nil, argError("watermark image must be bytes or a path; the API does not fetch image URLs")
		}
		fields["image_base64"] = b64
	}
	return s.binary(ctx, "watermark", file, fields, p, opts)
}

func (s *PDFService) Protect(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "protect", file, nil, params, opts)
}

func (s *PDFService) Unlock(ctx context.Context, file FileInput, password string, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "unlock", file, map[string]any{"password": password}, params, opts)
}

func (s *PDFService) Redact(ctx context.Context, file FileInput, regions []RedactRegion, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "redact", file, map[string]any{"regions": regions}, params, opts)
}

func (s *PDFService) PageNumbers(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "page-numbers", file, nil, params, opts)
}

func (s *PDFService) RemovePages(ctx context.Context, file FileInput, pages string, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "remove-pages", file, map[string]any{"pages": pages}, params, opts)
}

func (s *PDFService) Reorder(ctx context.Context, file FileInput, order []int, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "reorder", file, map[string]any{"order": order}, params, opts)
}

func (s *PDFService) Metadata(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "metadata", file, nil, params, opts)
}

func (s *PDFService) FormsFill(ctx context.Context, file FileInput, fields map[string]string, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "forms/fill", file, map[string]any{"fields": fields}, params, opts)
}

func (s *PDFService) ToImages(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "to-images", file, nil, params, opts)
}

func (s *PDFService) Info(ctx context.Context, file FileInput, opts *RequestOptions) (map[string]any, error) {
	resp, err := s.call(ctx, "info", file, nil, nil, opts)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, decodeData(resp, &out)
}

func (s *PDFService) FormsExtract(ctx context.Context, file FileInput, opts *RequestOptions) (*FormsExtractResult, error) {
	resp, err := s.call(ctx, "forms/extract", file, nil, nil, opts)
	if err != nil {
		return nil, err
	}
	out := &FormsExtractResult{}
	return out, decodeData(resp, out)
}
