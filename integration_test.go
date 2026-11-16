package sigli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func integrationClient(t *testing.T) *Client {
	t.Helper()
	key, base := os.Getenv("SIGLI_API_KEY"), os.Getenv("SIGLI_BASE_URL")
	if key == "" || base == "" {
		t.Skip("set SIGLI_API_KEY and SIGLI_BASE_URL to run integration tests")
	}
	c, err := NewClient(key, WithBaseURL(base), WithTimeout(120*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func corpus(t *testing.T, rel string) string {
	dir := os.Getenv("SIGLI_QA_CORPUS")
	if dir == "" {
		dir = filepath.Join("..", "..", ".qa-corpus")
	}
	p := filepath.Join(dir, rel)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("corpus file missing: %s", p)
	}
	return p
}

func TestIntegrationHTMLToPDF(t *testing.T) {
	c := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := c.HTMLToPDF(ctx, "<h1>Sigli SDK smoke</h1><p>Invoice 1042</p>", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsBinary() || !strings.HasPrefix(string(res.Bytes), "%PDF") {
		t.Fatalf("not a PDF: content-type=%s", res.ContentType)
	}
	t.Logf("html-to-pdf: %d bytes, request_id=%s", len(res.Bytes), res.RequestID)
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Log("pdftotext not on PATH: text-extraction assertion NOT run")
		return
	}
	f := filepath.Join(t.TempDir(), "smoke.pdf")
	os.WriteFile(f, res.Bytes, 0o644)
	out, err := exec.Command("pdftotext", f, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Invoice 1042") {
		t.Fatalf("pdftotext output missing text: %q", out)
	}
	t.Log("pdftotext extracted 'Invoice 1042'")
}

func TestIntegrationMerge(t *testing.T) {
	c := integrationClient(t)
	a, b := corpus(t, "docs/fw9.pdf"), corpus(t, "docs/f1040.pdf")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "merged.pdf")
	res, err := c.PDF.Merge(ctx, []FileInput{FilePath(a), FilePath(b)}, nil, &RequestOptions{OutputPath: out})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(res.Path)
	if !strings.HasPrefix(string(data), "%PDF") {
		t.Fatal("merged output is not a PDF")
	}
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Logf("pdfinfo not on PATH: page-count assertion NOT run (size=%d)", len(data))
		return
	}
	info, err := exec.Command("pdfinfo", res.Path).Output()
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Pages:\s+(\d+)`).FindStringSubmatch(string(info))
	if m == nil || m[1] != "8" {
		t.Fatalf("expected 8 pages (fw9=6 + f1040=2), pdfinfo said %v", m)
	}
	t.Logf("merged PDF streamed to file: %d bytes, 8 pages", len(data))
}

func TestIntegrationOCR(t *testing.T) {
	c := integrationClient(t)
	scan := corpus(t, "scans/nara.pdf")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	data, err := c.OCR(ctx, FilePath(scan), nil, nil)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && strings.Contains(e.Message, "--features ocr") {
			t.Skipf("backend built without OCR support: %s", e.Message)
		}
		t.Fatal(err)
	}
	text, _ := data["text"].(string)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("OCR returned no text: %v", data)
	}
	t.Logf("OCR text: %d chars", len(text))
}
