package sigli

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorded struct {
	Method string
	Path   string
	Header http.Header
	Body   map[string]any
	Query  string
}

type mock struct {
	t     *testing.T
	srv   *httptest.Server
	mu    sync.Mutex
	reqs  []recorded
	steps []http.HandlerFunc
}

func newMock(t *testing.T, steps ...http.HandlerFunc) *mock {
	m := &mock{t: t, steps: steps}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		m.mu.Lock()
		i := len(m.reqs)
		m.reqs = append(m.reqs, recorded{r.Method, r.URL.Path, r.Header.Clone(), body, r.URL.RawQuery})
		m.mu.Unlock()
		if i >= len(m.steps) {
			i = len(m.steps) - 1
		}
		m.steps[i](w, r)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mock) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.reqs) }
func (m *mock) req(i int) recorded {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reqs[i]
}

func jsonReply(status int, body string, hdr ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(hdr); i += 2 {
			w.Header().Set(hdr[i], hdr[i+1])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

func pdfReply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Request-Id", "req-pdf")
	io.WriteString(w, "%PDF-1.7 fake")
}

func hangUp(w http.ResponseWriter, r *http.Request) {
	hj, _ := w.(http.Hijacker)
	conn, _, _ := hj.Hijack()
	conn.Close()
}

func testClient(t *testing.T, m *mock, opts ...Option) (*Client, *[]time.Duration) {
	c, err := NewClient("sig_test_key", append([]Option{WithBaseURL(m.srv.URL + "/")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	var sleeps []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	return c, &sleeps
}

func asErr(t *testing.T, err error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *sigli.Error, got %T %v", err, err)
	}
	return e
}

var ctx = context.Background()

func TestNewClientRequiresKey(t *testing.T) {
	t.Setenv("SIGLI_API_KEY", "")
	if _, err := NewClient(""); asErr(t, err).Code != "missing_api_key" {
		t.Fatal("expected missing_api_key")
	}
}

func TestHeadersAndJSONUnwrap(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"data":[{"id":"w1","url":"https://x","events":["batch.completed"]}]}`))
	c, _ := testClient(t, m)
	hooks, err := c.Webhooks.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 || hooks[0].ID != "w1" || hooks[0].Events[0] != "batch.completed" {
		t.Fatalf("unexpected %+v", hooks)
	}
	r := m.req(0)
	if r.Path != "/webhooks" || r.Header.Get("X-API-Key") != "sig_test_key" || r.Header.Get("User-Agent") != "sigli-go/"+Version {
		t.Fatalf("bad request %+v", r)
	}
}

func TestProcessBinaryAndConvertType(t *testing.T) {
	m := newMock(t, pdfReply)
	c, _ := testClient(t, m)
	res, err := c.HTMLToPDF(ctx, "<h1>x</h1>", map[string]any{"format": "A4"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsBinary() || string(res.Bytes[:4]) != "%PDF" || res.RequestID != "req-pdf" {
		t.Fatalf("bad result %+v", res)
	}
	r := m.req(0)
	if r.Path != "/v1/process" || r.Header.Get("X-Convert-Type") != "html-to-pdf" || r.Body["source"] != "<h1>x</h1>" || r.Body["format"] != "A4" {
		t.Fatalf("bad request %+v", r)
	}
}

func TestProcessJSONData(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"meta":{"type":"html-to-pdf"},"data":{"url":"https://s3/x.pdf","pages":1}}`))
	c, _ := testClient(t, m)
	res, err := c.Process(ctx, "html-to-pdf", map[string]any{"source": "x", "return_url": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var d struct{ URL string }
	if res.IsBinary() || res.DecodeData(&d) != nil || d.URL != "https://s3/x.pdf" {
		t.Fatalf("bad %+v", res)
	}
}

func TestOutputPathStreamsToFile(t *testing.T) {
	m := newMock(t, pdfReply)
	c, _ := testClient(t, m)
	out := filepath.Join(t.TempDir(), "o.pdf")
	res, err := c.URLToPDF(ctx, "https://example.com", nil, &RequestOptions{OutputPath: out})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if res.Path != out || res.Bytes != nil || string(b) != "%PDF-1.7 fake" {
		t.Fatalf("bad %+v %q", res, b)
	}
}

func TestErrorMapping(t *testing.T) {
	m := newMock(t, jsonReply(400, `{"success":false,"code":"bad_request","message":"pages is required"}`, "X-Request-Id", "req-123"))
	c, _ := testClient(t, m)
	_, err := c.PDF.Split(ctx, FileBytes([]byte("%PDF")), "", nil, nil)
	e := asErr(t, err)
	if e.Status != 400 || e.Code != "bad_request" || e.Message != "pages is required" || e.RequestID != "req-123" {
		t.Fatalf("bad error %+v", e)
	}
	if m.count() != 1 {
		t.Fatalf("400 must not retry, got %d attempts", m.count())
	}
	if !strings.Contains(e.Error(), "req-123") {
		t.Fatalf("error string lacks request id: %s", e)
	}
}

func TestEmptyBody404(t *testing.T) {
	m := newMock(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	c, _ := testClient(t, m)
	_, err := c.Request(ctx, "GET", "/v1/nothing", nil, nil)
	e := asErr(t, err)
	if e.Status != 404 || e.Code != "http_404" || e.Message != "HTTP 404" {
		t.Fatalf("bad %+v", e)
	}
}

func TestRetryOn503ThenSuccess(t *testing.T) {
	m := newMock(t, jsonReply(503, `{"success":false,"code":"upstream","message":"busy"}`), jsonReply(502, ``), jsonReply(200, `{"success":true,"data":[]}`))
	c, sleeps := testClient(t, m)
	if _, err := c.Batch.List(ctx); err != nil {
		t.Fatal(err)
	}
	if m.count() != 3 || len(*sleeps) != 2 {
		t.Fatalf("attempts=%d sleeps=%v", m.count(), *sleeps)
	}

	if s := (*sleeps)[0]; s < 250*time.Millisecond || s > 500*time.Millisecond {
		t.Fatalf("backoff 0 out of range: %v", s)
	}
	if s := (*sleeps)[1]; s < 500*time.Millisecond || s > time.Second {
		t.Fatalf("backoff 1 out of range: %v", s)
	}
}

func TestRetryAfterHonored(t *testing.T) {
	m := newMock(t, jsonReply(429, `{"success":false,"code":"rate_limited","message":"slow down"}`, "Retry-After", "7"), jsonReply(200, `{"success":true,"data":[]}`))
	c, sleeps := testClient(t, m)
	if _, err := c.Workflows.List(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*sleeps) != 1 || (*sleeps)[0] != 7*time.Second {
		t.Fatalf("sleeps=%v", *sleeps)
	}
}

func TestRetryAfterHTTPDateAndCap(t *testing.T) {
	if d := parseRetryAfter(time.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat)); d != 60*time.Second {
		t.Fatalf("cap failed: %v", d)
	}
	if d := parseRetryAfter(time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)); d < 28*time.Second || d > 30*time.Second {
		t.Fatalf("date parse failed: %v", d)
	}
}

func TestNoRetryOnQuotaExceededOrFeatureUnavailable(t *testing.T) {
	for _, body := range []string{
		`{"success":false,"code":"quota_exceeded","message":"pool empty"}`,
	} {
		m := newMock(t, jsonReply(429, body))
		c, _ := testClient(t, m)
		_, err := c.Batch.List(ctx)
		if asErr(t, err).Code != "quota_exceeded" || m.count() != 1 {
			t.Fatalf("quota_exceeded retried: %d", m.count())
		}
	}
	m := newMock(t, jsonReply(503, `{"success":false,"code":"feature_unavailable","message":"no AI"}`))
	c, _ := testClient(t, m)
	_, err := c.Batch.List(ctx)
	if asErr(t, err).Code != "feature_unavailable" || m.count() != 1 {
		t.Fatalf("feature_unavailable retried: %d", m.count())
	}
}

func TestPostWithoutIdempotencyKeyNotRetried(t *testing.T) {
	m := newMock(t, jsonReply(503, `{"success":false,"code":"upstream_error","message":"x"}`), pdfReply)
	c, _ := testClient(t, m)
	_, err := c.HTMLToPDF(ctx, "x", nil, nil)
	if asErr(t, err).Status != 503 || m.count() != 1 {
		t.Fatalf("POST retried without key: %d", m.count())
	}
}

func TestPostWithIdempotencyKeyRetriedSameKey(t *testing.T) {
	m := newMock(t, jsonReply(503, `{}`), jsonReply(504, ``), pdfReply)
	c, _ := testClient(t, m)
	res, err := c.HTMLToPDF(ctx, "x", nil, &RequestOptions{IdempotencyKey: "inv-1042"})
	if err != nil || !res.IsBinary() {
		t.Fatal(err)
	}
	if m.count() != 3 {
		t.Fatalf("attempts=%d", m.count())
	}
	for i := 0; i < 3; i++ {
		if k := m.req(i).Header.Get("Idempotency-Key"); k != "inv-1042" {
			t.Fatalf("attempt %d key=%q", i, k)
		}
	}
}

func TestInvalidIdempotencyKey(t *testing.T) {
	m := newMock(t, pdfReply)
	c, _ := testClient(t, m)
	_, err := c.HTMLToPDF(ctx, "x", nil, &RequestOptions{IdempotencyKey: strings.Repeat("k", 129)})
	if asErr(t, err).Code != "invalid_idempotency_key" || m.count() != 0 {
		t.Fatal("expected client-side rejection")
	}
}

func TestGetRetriedOnNetworkError(t *testing.T) {
	m := newMock(t, hangUp, jsonReply(200, `{"success":true,"data":{"batch_id":"b1","status":"completed"}}`))
	c, _ := testClient(t, m)
	b, err := c.Batch.Get(ctx, "b1")
	if err != nil || b.Status != "completed" || m.count() != 2 {
		t.Fatalf("err=%v count=%d", err, m.count())
	}
}

func TestNetworkErrorCode(t *testing.T) {
	m := newMock(t, hangUp)
	c, sleeps := testClient(t, m, WithMaxRetries(1))
	_, err := c.Batch.Get(ctx, "b1")
	e := asErr(t, err)
	if e.Status != 0 || e.Code != "network_error" || m.count() != 2 || len(*sleeps) != 1 {
		t.Fatalf("e=%+v count=%d", e, m.count())
	}
}

func TestTimeoutCode(t *testing.T) {
	m := newMock(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond); pdfReply(w, r) })
	c, _ := testClient(t, m, WithMaxRetries(0), WithTimeout(50*time.Millisecond))
	_, err := c.Batch.List(ctx)
	if asErr(t, err).Code != "timeout" {
		t.Fatalf("got %v", err)
	}
}

func TestMaxRetriesRespected(t *testing.T) {
	m := newMock(t, jsonReply(502, ``))
	c, _ := testClient(t, m, WithMaxRetries(4))
	_, err := c.Batch.List(ctx)
	if asErr(t, err).Code != "http_502" || m.count() != 5 {
		t.Fatalf("attempts=%d", m.count())
	}
}

func TestFileInputs(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"data":{"text":"hi"}}`))
	c, _ := testClient(t, m)
	p := filepath.Join(t.TempDir(), "scan.pdf")
	os.WriteFile(p, []byte("%PDF-path"), 0o644)

	if _, err := c.OCR(ctx, FileBytes([]byte("%PDF-bytes")), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OCR(ctx, FilePath(p), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PDFToMarkdown(ctx, FileURL("https://example.com/a.pdf"), map[string]any{"use_ai": true}, nil); err != nil {
		t.Fatal(err)
	}
	dec := func(i int) string {
		b, _ := base64.StdEncoding.DecodeString(m.req(i).Body["file_base64"].(string))
		return string(b)
	}
	if dec(0) != "%PDF-bytes" || dec(1) != "%PDF-path" {
		t.Fatal("base64 file content mismatch")
	}
	r := m.req(2)
	if r.Body["file_url"] != "https://example.com/a.pdf" || r.Body["file_base64"] != nil || r.Body["use_ai"] != true || r.Header.Get("X-Convert-Type") != "pdf-to-markdown" {
		t.Fatalf("bad url request %+v", r)
	}
	if _, err := c.OCR(ctx, FilePath("/no/such/file"), nil, nil); asErr(t, err).Code != "invalid_argument" {
		t.Fatal("expected invalid_argument for missing path")
	}
}

func TestMergeFieldsAndOrdering(t *testing.T) {
	m := newMock(t, pdfReply)
	c, _ := testClient(t, m)
	if _, err := c.PDF.Merge(ctx, []FileInput{FileBytes([]byte("a")), FileBytes([]byte("b")), FileURL("https://x/c.pdf")}, nil, nil); err != nil {
		t.Fatal(err)
	}
	r := m.req(0)
	if r.Path != "/v1/pdf/merge" || len(r.Body["files"].([]any)) != 2 || len(r.Body["file_urls"].([]any)) != 1 {
		t.Fatalf("bad merge %+v", r.Body)
	}
	_, err := c.PDF.Merge(ctx, []FileInput{FileURL("https://x/a.pdf"), FileBytes([]byte("b"))}, nil, nil)
	if asErr(t, err).Code != "invalid_argument" {
		t.Fatal("expected ordering error")
	}
	if _, err := c.PDF.Merge(ctx, []FileInput{FileBytes([]byte("a"))}, nil, nil); asErr(t, err).Code != "invalid_argument" {
		t.Fatal("expected <2 files error")
	}
	if m.count() != 1 {
		t.Fatal("invalid merges must not hit the server")
	}
}

func TestPDFToolFieldNames(t *testing.T) {
	m := newMock(t, pdfReply)
	c, _ := testClient(t, m)
	f := FileBytes([]byte("%PDF"))
	c.PDF.Watermark(ctx, f, map[string]any{"text": "DRAFT", "image": FileBytes([]byte("png"))}, nil)
	c.PDF.Redact(ctx, f, []RedactRegion{{Page: 1, X: 1, Y: 2, Width: 3, Height: 4}}, nil, nil)
	c.PDF.Reorder(ctx, f, []int{3, 1, 2}, nil, nil)
	c.PDF.FormsFill(ctx, f, map[string]string{"name": "Ada"}, nil, nil)
	c.PDF.RemovePages(ctx, f, "2", nil, nil)
	c.PDF.PageNumbers(ctx, f, nil, nil)
	c.PDF.Unlock(ctx, f, "pw", nil, nil)
	want := []string{"/v1/pdf/watermark", "/v1/pdf/redact", "/v1/pdf/reorder", "/v1/pdf/forms/fill", "/v1/pdf/remove-pages", "/v1/pdf/page-numbers", "/v1/pdf/unlock"}
	for i, p := range want {
		if m.req(i).Path != p {
			t.Fatalf("req %d path %s want %s", i, m.req(i).Path, p)
		}
	}
	if m.req(0).Body["image_base64"] != base64.StdEncoding.EncodeToString([]byte("png")) || m.req(0).Body["image"] != nil {
		t.Fatal("watermark image not converted")
	}
	if reg := m.req(1).Body["regions"].([]any)[0].(map[string]any); reg["width"] != 3.0 {
		t.Fatalf("bad region %v", reg)
	}
	if m.req(3).Body["fields"].(map[string]any)["name"] != "Ada" || m.req(6).Body["password"] != "pw" {
		t.Fatal("bad fields/password")
	}
	if _, err := c.PDF.Watermark(ctx, f, map[string]any{"image": FileURL("https://x/logo.png")}, nil); asErr(t, err).Code != "invalid_argument" {
		t.Fatal("expected image URL rejection")
	}
}

func TestBatchSubmitHeaders(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"data":{"batch_id":"b1","status":"accepted","total_items":2}}`))
	c, _ := testClient(t, m)
	sub, err := c.Batch.Submit(ctx, []map[string]any{{"source_url": "https://a"}, {"file": FileBytes([]byte("x"))}}, &BatchSubmitOptions{ConvertType: "url-to-pdf", WebhookURL: "https://h"})
	if err != nil || sub.BatchID != "b1" {
		t.Fatal(err)
	}
	r := m.req(0)
	items := r.Body["items"].([]any)
	if r.Path != "/v1/batch/submit" || r.Header.Get("X-Convert-Type") != "url-to-pdf" || r.Body["webhook_url"] != "https://h" || items[1].(map[string]any)["file_base64"] != "eA==" {
		t.Fatalf("bad submit %+v", r)
	}
	c.Batch.Submit(ctx, []map[string]any{{"type": "ocr"}}, &BatchSubmitOptions{Mixed: true})
	if m.req(1).Header.Get("X-Batch-Mode") != "mixed" || m.req(1).Header.Get("X-Convert-Type") != "" {
		t.Fatal("mixed header missing")
	}
	if _, err := c.Batch.Submit(ctx, make([]map[string]any, 501), nil); asErr(t, err).Code != "invalid_argument" {
		t.Fatal("expected 501-item rejection")
	}
}

func TestWaitForBatch(t *testing.T) {
	m := newMock(t,
		jsonReply(200, `{"success":true,"data":{"batch_id":"b1","status":"accepted"}}`),
		jsonReply(200, `{"success":true,"data":{"batch_id":"b1","status":"processing"}}`),
		jsonReply(200, `{"success":true,"data":{"batch_id":"b1","status":"completed_with_errors","results":[{"index":0,"status":"failed","error":"boom"}]}}`))
	c, sleeps := testClient(t, m)
	b, err := c.WaitForBatch(ctx, "b1", &WaitOptions{PollInterval: time.Second, MaxInterval: 1200 * time.Millisecond})
	if err != nil || b.Status != "completed_with_errors" || b.Results[0].Error != "boom" {
		t.Fatalf("err=%v b=%+v", err, b)
	}
	if len(*sleeps) != 2 || (*sleeps)[0] != time.Second || (*sleeps)[1] != 1200*time.Millisecond {
		t.Fatalf("poll intervals %v", *sleeps)
	}
}

func TestWaitForBatchTimeout(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"data":{"batch_id":"b1","status":"processing"}}`))
	c, _ := testClient(t, m)
	c.sleep = sleepCtx
	_, err := c.Batch.Wait(ctx, "b1", &WaitOptions{PollInterval: 10 * time.Millisecond, Timeout: 60 * time.Millisecond})
	if asErr(t, err).Code != "batch_timeout" || m.count() < 2 {
		t.Fatalf("err=%v polls=%d", err, m.count())
	}
}

func TestWorkflowsAndWebhooks(t *testing.T) {
	m := newMock(t,
		jsonReply(200, `{"success":true,"data":{"workflow_id":"wf_1","name":"wm","version":1,"step_count":2}}`),
		jsonReply(200, `{"success":false,"data":{"run_id":"run_1","status":"completed_with_errors","steps":[],"error":"step 0 failed"}}`),
		jsonReply(200, `{"success":true,"data":{"id":"h1","url":"https://h","events":["*"],"secret":"s3cr3t"}}`),
		jsonReply(200, `{"success":true}`))
	c, _ := testClient(t, m)
	wf, err := c.Workflows.Create(ctx, CreateWorkflowParams{Name: "wm", Steps: []WorkflowStep{
		{Op: "html-to-pdf", OpKind: "convert", InputFrom: FromTrigger()},
		{Op: "watermark", OpKind: "pdf_tools", InputFrom: FromStep(0), Params: map[string]any{"text": "DRAFT"}},
	}}, nil)
	if err != nil || wf.WorkflowID != "wf_1" || wf.StepCount != 2 {
		t.Fatal(err)
	}
	steps := m.req(0).Body["steps"].([]any)
	if in := steps[1].(map[string]any)["input_from"].(map[string]any); in["source"] != "step" || in["step_index"] != 0.0 {
		t.Fatalf("bad input_from %v", in)
	}
	run, err := c.Workflows.Run(ctx, "wf_1", map[string]any{"file": FileURL("https://x/a.pdf")}, &RunOptions{Tag: "t"})
	if err != nil || run.Status != "completed_with_errors" || *run.Error != "step 0 failed" {
		t.Fatalf("success:false 200 must not error: %v", err)
	}
	if r := m.req(1); r.Path != "/v1/workflows/wf_1/run" || r.Body["input"].(map[string]any)["file_url"] != "https://x/a.pdf" || r.Body["tag"] != "t" {
		t.Fatalf("bad run %+v", r)
	}
	h, err := c.Webhooks.Create(ctx, "https://h", []string{"*"}, nil)
	if err != nil || h.Secret != "s3cr3t" {
		t.Fatal(err)
	}
	if err := c.Webhooks.Delete(ctx, "h1"); err != nil || m.req(3).Method != "DELETE" || m.req(3).Path != "/webhooks/h1" {
		t.Fatal("bad delete")
	}
}

func TestDeleteIsRetried(t *testing.T) {
	m := newMock(t, jsonReply(503, `{}`), jsonReply(200, `{"success":true}`))
	c, _ := testClient(t, m)
	if err := c.Webhooks.Delete(ctx, "h1"); err != nil || m.count() != 2 {
		t.Fatalf("err=%v count=%d", err, m.count())
	}
}

type vector struct {
	Secret          string `json:"secret"`
	Body            string `json:"body"`
	SignatureHeader string `json:"signature_header"`
}

func loadVector(t *testing.T) vector {
	raw, err := os.ReadFile("testdata/webhook_vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyWebhookSignatureVector(t *testing.T) {
	v := loadVector(t)
	body := []byte(v.Body)
	bare := strings.TrimPrefix(v.SignatureHeader, "sha256=")
	cases := []struct {
		name   string
		body   []byte
		header string
		secret string
		want   bool
	}{
		{"valid", body, v.SignatureHeader, v.Secret, true},
		{"bare hex", body, bare, v.Secret, true},
		{"uppercase hex", body, "sha256=" + strings.ToUpper(bare), v.Secret, true},
		{"whitespace", body, "  " + v.SignatureHeader + " ", v.Secret, true},
		{"tampered body", []byte(strings.Replace(v.Body, "completed", "complete!", 1)), v.SignatureHeader, v.Secret, false},
		{"wrong secret", body, v.SignatureHeader, v.Secret + "x", false},
		{"empty header", body, "", v.Secret, false},
		{"empty secret", body, v.SignatureHeader, "", false},
		{"garbage", body, "sha256=zzzz", v.Secret, false},
		{"truncated", body, v.SignatureHeader[:20], v.Secret, false},
	}
	for _, tc := range cases {
		if got := VerifyWebhookSignature(tc.body, tc.header, tc.secret); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}

	decoded, _ := hex.DecodeString(v.Secret)
	if VerifyWebhookSignature(body, v.SignatureHeader, string(decoded)) {
		t.Error("hex-decoded secret must not verify")
	}
}
