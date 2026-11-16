package sigli

import (
	"encoding/base64"
	"testing"
)

func TestDocumentTrustPDFPathsAndFields(t *testing.T) {
	m := newMock(t, pdfReply)
	c, _ := testClient(t, m)
	f := FileBytes([]byte("%PDF"))
	if res, err := c.PDF.PDFA(ctx, f, map[string]any{"level": "1b", "title": "Archive", "validate": false}, nil); err != nil || string(res.Bytes) != "%PDF-1.7 fake" {
		t.Fatalf("pdfa: %v", err)
	}
	if _, err := c.PDF.Sign(ctx, f, []byte("P12"), "pw", map[string]any{"reason": "Approved", "visible": map[string]any{"page": 1, "x": 36, "y": 36}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PDF.DetectFormFields(ctx, FileURL("https://x.test/w4.pdf"), map[string]any{"min_confidence": 0.8, "output": "pdf"}, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"/v1/pdf/pdfa", "/v1/pdf/sign", "/v1/pdf/forms/detect"}
	for i, p := range want {
		if m.req(i).Path != p || m.req(i).Method != "POST" {
			t.Fatalf("req %d %s %s want %s", i, m.req(i).Method, m.req(i).Path, p)
		}
	}
	b := m.req(0).Body
	if b["level"] != "1b" || b["title"] != "Archive" || b["validate"] != false || b["file_base64"] != base64.StdEncoding.EncodeToString([]byte("%PDF")) {
		t.Fatalf("bad pdfa body %v", b)
	}
	b = m.req(1).Body
	if b["certificate_base64"] != base64.StdEncoding.EncodeToString([]byte("P12")) || b["certificate_password"] != "pw" || b["reason"] != "Approved" {
		t.Fatalf("bad sign body %v", b)
	}
	if v := b["visible"].(map[string]any); v["x"] != 36.0 {
		t.Fatalf("bad visible %v", v)
	}
	b = m.req(2).Body
	if b["file_url"] != "https://x.test/w4.pdf" || b["min_confidence"] != 0.8 || b["output"] != "pdf" {
		t.Fatalf("bad detect body %v", b)
	}
	if _, err := c.PDF.Sign(ctx, f, nil, "pw", nil, nil); asErr(t, err).Code != "invalid_argument" {
		t.Fatal("expected missing certificate rejection")
	}
}

func TestCompareMapsOldAndNew(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"data":{"summary":{"changes":3,"similarity":0.85,"changed_pages_new":[1]},"changes":[{"kind":"replace","old_text":"2018","new_text":"2024","old_page":1,"new_page":1,"new_rects":[{"page":1,"x0":1,"y0":2,"x1":3,"y1":4}],"old_rects":[]}],"truncated":false}}`))
	c, _ := testClient(t, m)
	res, err := c.PDF.Compare(ctx, FileBytes([]byte("%PDF-old")), FileURL("https://x.test/new.pdf"), map[string]any{"ignore_case": true}, nil)
	if err != nil || res.IsBinary() {
		t.Fatalf("compare: %v", err)
	}
	var cmp CompareResult
	if err := res.DecodeData(&cmp); err != nil || cmp.Summary.Changes != 3 || cmp.Changes[0].NewText != "2024" || cmp.Changes[0].NewRects[0].X1 != 3 {
		t.Fatalf("decode: %v %+v", err, cmp)
	}
	r := m.req(0)
	if r.Path != "/v1/pdf/compare" || r.Body["old_file_base64"] != base64.StdEncoding.EncodeToString([]byte("%PDF-old")) || r.Body["new_file_url"] != "https://x.test/new.pdf" || r.Body["ignore_case"] != true {
		t.Fatalf("bad compare body %v", r.Body)
	}
	if _, ok := r.Body["file_base64"]; ok {
		t.Fatal("compare must not send file_base64")
	}
	if _, err := c.PDF.Compare(ctx, nil, FileBytes([]byte("x")), nil, nil); asErr(t, err).Code != "invalid_argument" {
		t.Fatal("expected missing old file rejection")
	}
}

func TestComparePDFOutputIsBinary(t *testing.T) {
	m := newMock(t, pdfReply)
	c, _ := testClient(t, m)
	res, err := c.PDF.Compare(ctx, FileBytes([]byte("a")), FileBytes([]byte("b")), map[string]any{"output": "pdf", "cover": false}, nil)
	if err != nil || !res.IsBinary() || m.req(0).Body["cover"] != false {
		t.Fatalf("redline: %v", err)
	}
}

func TestVerifySignaturesDecodes(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"data":{"signed":true,"all_valid":true,"signatures":[{"field_name":"Signature1","signer":"Asha Rao","integrity_valid":true,"certificate_trusted":false,"covers_whole_document":true,"error":null}]}}`))
	c, _ := testClient(t, m)
	v, err := c.PDF.VerifySignatures(ctx, FileBytes([]byte("%PDF")), nil)
	if err != nil || !v.Signed || !v.AllValid || *v.Signatures[0].Signer != "Asha Rao" || v.Signatures[0].CertificateTrusted {
		t.Fatalf("verify: %v %+v", err, v)
	}
	if m.req(0).Path != "/v1/pdf/signatures/verify" {
		t.Fatal(m.req(0).Path)
	}
}

func TestAuditListAndVerify(t *testing.T) {
	m := newMock(t,
		jsonReply(200, `{"success":true,"data":{"events":[{"seq":5,"action":"pdf.sign","details":{"signer":"A"}}],"next_before_seq":5}}`),
		jsonReply(200, `{"success":true,"data":{"events":[],"next_before_seq":null}}`),
		jsonReply(200, `{"success":true,"data":{"events_checked":5,"intact":false,"tampered_seqs":[3],"missing_seqs":[[6,7]]}}`))
	c, _ := testClient(t, m)
	p, err := c.Audit.List(ctx, nil)
	if err != nil || p.Events[0].Seq != 5 || *p.NextBeforeSeq != 5 || string(p.Events[0].Details) != `{"signer":"A"}` {
		t.Fatalf("list: %v %+v", err, p)
	}
	p, err = c.Audit.List(ctx, &AuditListParams{Limit: 10, BeforeSeq: 42, Action: "pdf.sign"})
	if err != nil || p.NextBeforeSeq != nil {
		t.Fatal(err)
	}
	v, err := c.Audit.Verify(ctx)
	if err != nil || v.Intact || v.TamperedSeqs[0] != 3 || v.MissingSeqs[0] != [2]int64{6, 7} {
		t.Fatalf("verify: %v %+v", err, v)
	}
	if m.req(0).Path != "/v1/audit" || m.req(0).Query != "" {
		t.Fatalf("bad list %+v", m.req(0))
	}
	if m.req(1).Query != "action=pdf.sign&before_seq=42&limit=10" {
		t.Fatalf("bad query %q", m.req(1).Query)
	}
	if m.req(2).Path != "/v1/audit/verify" || m.req(2).Method != "GET" {
		t.Fatal("bad verify request")
	}
}

func TestWorkflowTriggersCRUD(t *testing.T) {
	m := newMock(t,
		jsonReply(200, `{"success":true,"data":{"trigger_id":"trg_1","token":"wht_x","signing_secret":"whsec_y","url":"https://api.sigli.dev/hooks/workflows/wht_x","require_signature":true,"rate_limit_per_minute":60}}`),
		jsonReply(200, `{"success":true,"data":[{"trigger_id":"trg_1","token_prefix":"wht_x","rate_limit_per_minute":60}]}`),
		jsonReply(200, `{"success":true,"data":{"trigger_id":"trg_1","revoked":true}}`))
	c, _ := testClient(t, m)
	tr, err := c.Workflows.Triggers.Create(ctx, "wf_1", CreateTriggerParams{Name: "zap", RequireSignature: true, RateLimitPerMinute: 60}, nil)
	if err != nil || tr.Token != "wht_x" || tr.SigningSecret != "whsec_y" || !tr.RequireSignature {
		t.Fatalf("create: %v %+v", err, tr)
	}
	list, err := c.Workflows.Triggers.List(ctx, "wf_1")
	if err != nil || len(list) != 1 || list[0].RateLimitPerMinute != 60 {
		t.Fatalf("list: %v", err)
	}
	if err := c.Workflows.Triggers.Revoke(ctx, "wf_1", "trg_1"); err != nil {
		t.Fatal(err)
	}
	methods := []string{"POST", "GET", "DELETE"}
	paths := []string{"/v1/workflows/wf_1/triggers", "/v1/workflows/wf_1/triggers", "/v1/workflows/wf_1/triggers/trg_1"}
	for i := range methods {
		if m.req(i).Method != methods[i] || m.req(i).Path != paths[i] {
			t.Fatalf("req %d %s %s", i, m.req(i).Method, m.req(i).Path)
		}
	}
	b := m.req(0).Body
	if b["name"] != "zap" || b["require_signature"] != true || b["rate_limit_per_minute"] != 60.0 || len(b) != 3 {
		t.Fatalf("bad create body %v", b)
	}
}

func TestComputeTriggerSignatureVector(t *testing.T) {
	body := []byte(`{"input":{"source":"<p>x</p>"}}`)
	want := "sha256=321d7d9a00aea72d6439e49cdeb4c1bfa3baf560fcb6c7edac183bf11d659997"
	if got := ComputeTriggerSignature("whsec_test", 1700000000, body); got != want {
		t.Fatalf("got %s", got)
	}
	if ComputeTriggerSignature("whsec_test", 1700000001, body) == want {
		t.Fatal("timestamp must change the signature")
	}
	if ComputeTriggerSignature("whsec_other", 1700000000, body) == want {
		t.Fatal("secret must change the signature")
	}
}

func TestOCRLanguageAutoPassthrough(t *testing.T) {
	m := newMock(t, jsonReply(200, `{"success":true,"data":{"text":"hi","language_detection":{"language":"eng","rotated_pages":[]}}}`))
	c, _ := testClient(t, m)
	data, err := c.OCR(ctx, FileBytes([]byte("%PDF")), map[string]any{"language": "auto"}, nil)
	if err != nil || data["language_detection"].(map[string]any)["language"] != "eng" {
		t.Fatalf("ocr: %v %v", err, data)
	}
	if m.req(0).Body["language"] != "auto" || m.req(0).Header.Get("X-Convert-Type") != "ocr" {
		t.Fatalf("bad ocr request %+v", m.req(0))
	}
}
