# sigli-go

Official Go client for the [Sigli](https://sigli.dev) document API: HTML/URL to PDF, screenshots, OCR, PDF to Markdown, PDF tools, AI extraction, batches, workflows and webhooks.

- Standard library only, Go 1.21+
- Retries with exponential backoff + jitter (429/502/503/504 and network errors), honors `Retry-After`
- Idempotency keys, typed `*sigli.Error`, streaming of large binary results to disk

## Install

<!-- install:begin -->
```bash
go get github.com/antariksh-labs/sigli-go
```

Go 1.21 or newer. This repository is a published copy of `sdks/go` in the Sigli monorepo; open issues and changes against Sigli.
<!-- install:end -->

## Quickstart

```go
package main

import (
	"context"
	"log"
	"os"

	sigli "github.com/antariksh-labs/sigli-go"
)

func main() {
	c, err := sigli.NewClient(os.Getenv("SIGLI_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	res, err := c.HTMLToPDF(context.Background(), "<h1>Hello</h1>", map[string]any{"format": "A4"}, nil)
	if err != nil {
		log.Fatal(err)
	}
	os.WriteFile("hello.pdf", res.Bytes, 0o644)
}
```

## Configuration

| Option | Default | Notes |
|---|---|---|
| `apiKey` (first arg) | `$SIGLI_API_KEY` | Sent as `X-API-Key`. `NewClient` errors if missing. |
| `WithBaseURL(u)` | `$SIGLI_BASE_URL`, else `https://api.sigli.dev` | Use `http://localhost:5500` for a local stack. |
| `WithTimeout(d)` | `60s` | Per attempt. Override per call with `RequestOptions.Timeout`. |
| `WithMaxRetries(n)` | `2` | Retries after the first attempt. |
| `WithHTTPClient(h)` | `&http.Client{}` | Custom transport, proxy, tracing. |

Every method takes a `context.Context` first and, where it makes sense, a `*sigli.RequestOptions` last (`nil` is fine):

```go
&sigli.RequestOptions{
	IdempotencyKey: "invoice-1042",  // Idempotency-Key header
	OutputPath:     "/tmp/out.pdf",  // stream binary body to disk, Result.Path is set
	Timeout:        2 * time.Minute,
	Headers:        map[string]string{"X-Request-Id": "my-trace-id"},
}
```

Operation parameters use the API's own JSON field names (`format`, `full_page`, `use_ai`, `level`, ...) in a `map[string]any`, so the API docs map one-to-one.

## Surface

| Area | Methods |
|---|---|
| Convert | `Process(ctx, convertType, params, opts)`, `HTMLToPDF`, `URLToPDF`, `Screenshot`, `OCR`, `PDFToMarkdown` |
| PDF (`c.PDF`) | `Merge`, `Split`, `Compress`, `Rotate`, `Watermark`, `Protect`, `Unlock`, `Redact`, `PageNumbers`, `RemovePages`, `Reorder`, `Metadata`, `Info`, `FormsExtract`, `FormsFill`, `ToImages`, `PDFA`, `Compare`, `Sign`, `VerifySignatures`, `DetectFormFields` |
| AI (`c.AI`) | `Summary`, `Structured`, `Translate`, `Query` |
| Batch (`c.Batch`) | `Submit`, `Get`, `List`, `Wait` (also `c.WaitForBatch`) |
| Workflows (`c.Workflows`) | `List`, `Get`, `Create`, `Run`, `Runs`; `c.Workflows.Triggers.Create`, `List`, `Revoke`; `sigli.ComputeTriggerSignature(secret, timestamp, body)` |
| Audit (`c.Audit`) | `List(ctx, &sigli.AuditListParams{Limit, BeforeSeq, Action})`, `Verify` (owner/admin) |
| Webhooks (`c.Webhooks`) | `List`, `Create`, `Delete`; `sigli.VerifyWebhookSignature(payload, header, secret)` |
| Anything else | `c.Request(ctx, method, path, body, opts)` returns the raw `*sigli.Response` |

Binary endpoints return `*sigli.Result`: `Bytes` (or `Path` when `OutputPath` was set). If the API answers JSON instead (for example you passed `"return_url": true`, or a `webhook` made the job async with HTTP 202), `Result.Data` holds the envelope's `data`; check `res.IsBinary()` and use `res.DecodeData(&v)`.

### File inputs

```go
sigli.FileBytes(b)                         // in memory, sent as file_base64
sigli.FilePath("scan.pdf")                 // read from disk, sent as file_base64
sigli.FileURL("https://example.com/a.pdf") // fetched by Sigli, sent as file_url
```

In `Process`, `Batch.Submit` items, and `Workflows.Run` input, put a `FileInput` under the `"file"` key and it is converted for you.

`PDF.Merge` note: the API merges all inline files first and URL files after them, so the SDK rejects a URL that comes before a bytes/path input (the output order would silently differ).

### Errors

```go
res, err := c.PDF.Compress(ctx, sigli.FilePath("big.pdf"), nil, nil)
var e *sigli.Error
if errors.As(err, &e) {
	log.Printf("status=%d code=%s msg=%s request_id=%s", e.Status, e.Code, e.Message, e.RequestID)
}
```

`Status` is 0 for client-side failures (`network_error`, `timeout`, `invalid_argument`, `invalid_idempotency_key`, `batch_timeout`). API codes include `bad_request`, `unauthorized`, `plan_upgrade_required` (402: the feature is not on your plan, e.g. batch, webhooks and AI on Starter), `quota_exceeded`, `rate_limited`, `upstream_error`, `feature_unavailable`. Unknown routes return 404 with an empty body, mapped to code `http_404`. `RequestID` comes from the `X-Request-Id` response header; include it in support requests.

### Retries and idempotency

- Retried: HTTP 429, 502, 503, 504, network errors and timeouts. Not retried: 429 `quota_exceeded` (monthly pool, waiting doesn't help) and 503 `feature_unavailable`.
- Backoff: `min(8s, 0.5s * 2^attempt)` with equal jitter. A `Retry-After` header (seconds or HTTP date, capped at 60s) takes precedence.
- `GET`/`DELETE` retry freely. `POST`/`PATCH` retry **only** with an `IdempotencyKey`, which is sent unchanged on every attempt. The API caches successful responses per key for 24h and replays them (`Idempotency-Replayed: true`), so a retried conversion is not billed twice.

## Examples

### 1. Invoice PDF from an HTML template

```go
tpl := template.Must(template.New("inv").Parse(`
<html><body style="font-family:sans-serif">
  <h1>Invoice {{.Number}}</h1><p>Bill to: {{.Customer}}</p>
  <table>{{range .Lines}}<tr><td>{{.Desc}}</td><td>{{printf "%.2f" .Amount}}</td></tr>{{end}}</table>
</body></html>`))
var buf bytes.Buffer
tpl.Execute(&buf, invoice)

res, err := c.HTMLToPDF(ctx, buf.String(),
	map[string]any{"format": "A4"},
	&sigli.RequestOptions{
		IdempotencyKey: "invoice-" + invoice.Number, // safe to retry, never double-billed
		OutputPath:     filepath.Join("invoices", invoice.Number+".pdf"),
	})
if err != nil {
	return err
}
log.Println("saved", res.Path)
```

### 2. Scanned-receipt OCR

```go
data, err := c.OCR(ctx, sigli.FilePath("receipts/2026-10-24-coffee.jpg"), nil, nil)
if err != nil {
	return err
}
text, _ := data["text"].(string)
total := regexp.MustCompile(`(?i)total\s*[:$]?\s*([\d.,]+)`).FindStringSubmatch(text)
fmt.Println("receipt text:", text, "total:", total)
```

### 3. Batch-render 500 URLs and wait

```go
urls := loadURLs() // up to thousands; the API takes 500 per batch
for start := 0; start < len(urls); start += sigli.MaxBatchItems {
	end := min(start+sigli.MaxBatchItems, len(urls))
	items := make([]map[string]any, 0, end-start)
	for _, u := range urls[start:end] {
		items = append(items, map[string]any{"source_url": u})
	}
	sub, err := c.Batch.Submit(ctx, items, &sigli.BatchSubmitOptions{
		ConvertType:    "url-to-pdf",
		IdempotencyKey: fmt.Sprintf("crawl-2026-10-25-%d", start),
	})
	if err != nil {
		return err
	}
	b, err := c.WaitForBatch(ctx, sub.BatchID, &sigli.WaitOptions{Timeout: 30 * time.Minute})
	if err != nil {
		return err
	}
	log.Printf("batch %s: %s (%d ok, %d failed)", b.BatchID, b.Status, b.ProcessedItems-b.FailedItems, b.FailedItems)
	for _, r := range b.Results {
		if r.Status == "failed" {
			log.Printf("item %d (%s) failed: %v", r.Index, urls[start+r.Index], r.Error)
		}
	}
}
```

### 4. Verify webhooks in a net/http handler

```go
secret := os.Getenv("SIGLI_WEBHOOK_SECRET") // from c.Webhooks.Create(...).Secret, shown once

http.HandleFunc("/hooks/sigli", func(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	// Verify the raw bytes; re-encoding parsed JSON would change them.
	if !sigli.VerifyWebhookSignature(body, r.Header.Get(sigli.SignatureHeader), secret) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var evt struct {
		Event     string          `json:"event"`
		Timestamp string          `json:"timestamp"`
		Data      json.RawMessage `json:"data"`
	}
	json.Unmarshal(body, &evt)
	log.Printf("%s at %s", r.Header.Get(sigli.EventHeader), evt.Timestamp)
	w.WriteHeader(http.StatusNoContent)
})
```

Signature: `X-Sigli-Signature: sha256=<hex HMAC-SHA256(secret, raw body)>`, where the key is the secret string exactly as returned (not hex-decoded). There is no timestamp in the signature, so if replay matters, reject stale `timestamp` values from the body and dedupe on `data.batch_id`. Only workspace webhooks registered via `Webhooks.Create` are signed; one-off `webhook` / `webhook_url` callbacks on individual requests are delivered **unsigned**.

### 5. Merge contracts and watermark them

```go
merged, err := c.PDF.Merge(ctx, []sigli.FileInput{
	sigli.FilePath("contracts/msa.pdf"),
	sigli.FilePath("contracts/sow-1.pdf"),
	sigli.FileURL("https://files.example.com/dpa.pdf"), // URLs must come last
}, nil, nil)
if err != nil {
	return err
}
_, err = c.PDF.Watermark(ctx, sigli.FileBytes(merged.Bytes), map[string]any{
	"text":     "CONFIDENTIAL - ACME CORP",
	"opacity":  0.2,
	"position": "center",
}, &sigli.RequestOptions{OutputPath: "contracts/acme-bundle.pdf"})
```

### 6. AI structured extraction from an invoice

```go
data, err := c.AI.Structured(ctx, sigli.FilePath("inbox/invoice-8812.pdf"),
	[]string{"invoice_number", "invoice_date", "vendor", "total", "currency"}, nil)
var e *sigli.Error
if errors.As(err, &e) && e.Code == "plan_upgrade_required" {
	log.Fatal("AI extraction is not on this plan")
} else if err != nil {
	return err
}
fields, _ := data["fields"].(map[string]any)
fmt.Println(fields["invoice_number"], fields["total"], fields["currency"])
```

### 7. Compare two revisions, sign, verify, detect form fields

```go
res, err := c.PDF.Compare(ctx, sigli.FilePath("fw9_2018.pdf"), sigli.FilePath("fw9_2024.pdf"), nil, nil)
var cmp sigli.CompareResult
res.DecodeData(&cmp)
fmt.Println(cmp.Summary.Changes, cmp.Summary.Similarity)

redline, _ := c.PDF.Compare(ctx, sigli.FilePath("fw9_2018.pdf"), sigli.FilePath("fw9_2024.pdf"),
	map[string]any{"output": "pdf"}, &sigli.RequestOptions{OutputPath: "redline.pdf"})

p12, _ := os.ReadFile("signer.p12")
signed, err := c.PDF.Sign(ctx, sigli.FilePath("contract.pdf"), p12, os.Getenv("P12_PASSWORD"),
	map[string]any{"reason": "Approved", "visible": map[string]any{"page": 1, "x": 36, "y": 36}}, nil)
report, err := c.PDF.VerifySignatures(ctx, sigli.FileBytes(signed.Bytes), nil)
fmt.Println(report.AllValid)

archived, err := c.PDF.PDFA(ctx, sigli.FilePath("report.pdf"), map[string]any{"level": "2b"}, nil)

det, err := c.PDF.DetectFormFields(ctx, sigli.FilePath("w4_flat.pdf"), map[string]any{"min_confidence": 0.6}, nil)
var fields sigli.FormDetection
det.DecodeData(&fields)
```

`PDFA`, `Sign` and `DetectFormFields` with `"output": "pdf"` return a binary `Result`; `Compare` returns JSON unless `"output": "pdf"`.

### 8. Fire a signed workflow trigger

```go
tr, err := c.Workflows.Triggers.Create(ctx, "wf_123", sigli.CreateTriggerParams{Name: "scanner", RequireSignature: true}, nil)
body, _ := os.ReadFile("scan.pdf")
ts := time.Now().Unix()
req, _ := http.NewRequest("POST", tr.URL, bytes.NewReader(body))
req.Header.Set("Content-Type", "application/pdf")
req.Header.Set(sigli.TriggerTimestampHeader, strconv.FormatInt(ts, 10))
req.Header.Set(sigli.TriggerSignatureHeader, sigli.ComputeTriggerSignature(tr.SigningSecret, ts, body))
```

`Token` and `SigningSecret` are returned only by `Create`. OCR language detection needs no new method: `c.OCR(ctx, file, map[string]any{"language": "auto"}, nil)` returns `language_detection` in the data.

## Testing

```bash
# unit tests (local httptest server, no network)
docker run --rm -v "$PWD/sdks/go":/src -w /src golang:1.23 go test ./...

# integration smoke against a running API (skipped unless both env vars are set)
docker run --rm -v "$PWD/sdks/go":/src -w /src \
  -v "$PWD/.qa-corpus":/corpus:ro -e SIGLI_QA_CORPUS=/corpus \
  -e SIGLI_API_KEY -e SIGLI_BASE_URL=http://host.docker.internal:5500 \
  -v /path/to/signer.p12:/pki/signer.p12:ro -e SIGLI_TEST_P12=/pki/signer.p12 -e SIGLI_TEST_P12_PASSWORD \
  golang:1.23 sh -c 'apt-get update -qq && apt-get install -y -qq poppler-utils >/dev/null; go test -v -run Integration ./...'
```

`testdata/webhook_vector.json` is a real delivery captured from the API, with the secret of a throwaway local workspace.
