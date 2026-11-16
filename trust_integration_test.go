package sigli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIntegrationCompareW9Revisions(t *testing.T) {
	c := integrationClient(t)
	a, b := corpus(t, "diff/fw9_2018.pdf"), corpus(t, "diff/fw9_2024.pdf")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := c.PDF.Compare(ctx, FilePath(a), FilePath(b), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var cmp CompareResult
	if err := res.DecodeData(&cmp); err != nil {
		t.Fatal(err)
	}
	s := cmp.Summary
	t.Logf("compare: pages %d->%d changes=%d inserted=%d deleted=%d similarity=%.4f", s.PagesOld, s.PagesNew, s.Changes, s.WordsInserted, s.WordsDeleted, s.Similarity)
	if s.Changes == 0 || s.Similarity <= 0 || s.Similarity >= 1 || len(cmp.Changes) == 0 {
		t.Fatalf("implausible comparison %+v", s)
	}
	red, err := c.PDF.Compare(ctx, FilePath(a), FilePath(b), map[string]any{"output": "pdf"}, nil)
	if err != nil || !strings.HasPrefix(string(red.Bytes), "%PDF") {
		t.Fatalf("redline: %v", err)
	}
	t.Logf("redline: %d bytes", len(red.Bytes))
}

func TestIntegrationSignThenVerify(t *testing.T) {
	c := integrationClient(t)
	p12 := os.Getenv("SIGLI_TEST_P12")
	if p12 == "" {
		t.Skip("set SIGLI_TEST_P12 and SIGLI_TEST_P12_PASSWORD to run signing")
	}
	cert, err := os.ReadFile(p12)
	if err != nil {
		t.Fatal(err)
	}
	doc := corpus(t, "diff/fw9_2024.pdf")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	signed, err := c.PDF.Sign(ctx, FilePath(doc), cert, os.Getenv("SIGLI_TEST_P12_PASSWORD"), map[string]any{"reason": "SDK smoke"}, nil)
	if err != nil || !strings.HasPrefix(string(signed.Bytes), "%PDF") {
		t.Fatalf("sign: %v", err)
	}
	v, err := c.PDF.VerifySignatures(ctx, FileBytes(signed.Bytes), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Signed || !v.AllValid || len(v.Signatures) == 0 || !v.Signatures[0].IntegrityValid || !v.Signatures[0].CoversWholeDocument {
		t.Fatalf("verification failed %+v", v)
	}
	signer := ""
	if v.Signatures[0].Signer != nil {
		signer = *v.Signatures[0].Signer
	}
	t.Logf("sign+verify: %d bytes, signed=%v all_valid=%v signer=%q trusted=%v", len(signed.Bytes), v.Signed, v.AllValid, signer, v.Signatures[0].CertificateTrusted)
}

func TestIntegrationDetectFormFields(t *testing.T) {
	c := integrationClient(t)
	w4 := corpus(t, "forms/irs_fw4_flat.pdf")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := c.PDF.DetectFormFields(ctx, FilePath(w4), nil, nil)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == "plan_upgrade_required" {
			t.Skipf("plan has no pdf-forms feature: %s", e.Message)
		}
		t.Fatal(err)
	}
	var d FormDetection
	if err := res.DecodeData(&d); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, f := range d.Fields {
		kinds[f.Type]++
	}
	t.Logf("forms detect: pages=%d count=%d by type=%v", d.Pages, d.Count, kinds)
	if d.Count == 0 || d.Count != len(d.Fields) {
		t.Fatalf("bad detection %d/%d", d.Count, len(d.Fields))
	}
}

func TestIntegrationAuditList(t *testing.T) {
	c := integrationClient(t)
	p, err := c.Audit.List(context.Background(), &AuditListParams{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events) > 5 {
		t.Fatalf("limit ignored: %d", len(p.Events))
	}
	t.Logf("audit: %d events", len(p.Events))
}

func TestIntegrationSignedTriggerFires(t *testing.T) {
	c := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	wf, err := c.Workflows.Create(ctx, CreateWorkflowParams{
		Name:  fmt.Sprintf("go-sdk-trigger-%d", time.Now().UnixNano()),
		Steps: []WorkflowStep{{Op: "html-to-pdf", OpKind: "convert", InputFrom: FromTrigger()}},
	}, nil)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == "plan_upgrade_required" {
			t.Skipf("plan has no workflows feature: %s", e.Message)
		}
		t.Fatal(err)
	}
	defer c.Request(context.Background(), http.MethodDelete, "/v1/workflows/"+url.PathEscape(wf.WorkflowID), nil, nil)
	tr, err := c.Workflows.Triggers.Create(ctx, wf.WorkflowID, CreateTriggerParams{Name: "go smoke", RequireSignature: true}, nil)
	if err != nil || !strings.HasPrefix(tr.SigningSecret, "whsec_") || !strings.HasPrefix(tr.Token, "wht_") {
		t.Fatalf("create trigger: %v", err)
	}
	u, _ := url.Parse(tr.URL)
	target := strings.TrimRight(os.Getenv("SIGLI_BASE_URL"), "/") + u.Path
	body, _ := json.Marshal(map[string]any{"input": map[string]any{"source": "<p>trigger smoke</p>"}})
	ts := time.Now().Unix()
	fire := func(sig string) (int, map[string]any) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(TriggerTimestampHeader, strconv.FormatInt(ts, 10))
		req.Header.Set(TriggerSignatureHeader, sig)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var env map[string]any
		json.NewDecoder(res.Body).Decode(&env)
		return res.StatusCode, env
	}
	if code, _ := fire("sha256=" + strings.Repeat("0", 64)); code != http.StatusUnauthorized {
		t.Fatalf("bad signature accepted: %d", code)
	}
	code, env := fire(ComputeTriggerSignature(tr.SigningSecret, ts, body))
	if code != http.StatusAccepted {
		t.Fatalf("signed fire: %d %v", code, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["status"] != "queued" {
		t.Fatalf("unexpected %v", env)
	}
	list, err := c.Workflows.Triggers.List(ctx, wf.WorkflowID)
	if err != nil || len(list) != 1 || list[0].TriggerID != tr.TriggerID {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if err := c.Workflows.Triggers.Revoke(ctx, wf.WorkflowID, tr.TriggerID); err != nil {
		t.Fatal(err)
	}
	list, _ = c.Workflows.Triggers.List(ctx, wf.WorkflowID)
	if len(list) != 0 {
		t.Fatal("trigger still listed after revoke")
	}
	t.Logf("trigger: bad sig 401, signed fire 202 run_id=%v, revoked", data["run_id"])
}
