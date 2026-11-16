package sigli

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

type CompareRect struct {
	Page int     `json:"page"`
	X0   float64 `json:"x0"`
	Y0   float64 `json:"y0"`
	X1   float64 `json:"x1"`
	Y1   float64 `json:"y1"`
}

type CompareChange struct {
	Kind     string        `json:"kind"`
	OldText  string        `json:"old_text,omitempty"`
	NewText  string        `json:"new_text,omitempty"`
	OldPage  *int          `json:"old_page"`
	NewPage  *int          `json:"new_page"`
	NewRects []CompareRect `json:"new_rects"`
	OldRects []CompareRect `json:"old_rects"`
}

type CompareSummary struct {
	PagesOld        int     `json:"pages_old"`
	PagesNew        int     `json:"pages_new"`
	WordsOld        int     `json:"words_old"`
	WordsNew        int     `json:"words_new"`
	WordsInserted   int     `json:"words_inserted"`
	WordsDeleted    int     `json:"words_deleted"`
	Changes         int     `json:"changes"`
	Similarity      float64 `json:"similarity"`
	ChangedPagesNew []int   `json:"changed_pages_new"`
	Approximate     bool    `json:"approximate"`
}

type CompareResult struct {
	Summary   CompareSummary  `json:"summary"`
	Changes   []CompareChange `json:"changes"`
	Truncated bool            `json:"truncated"`
}

type SignatureReport struct {
	FieldName           *string `json:"field_name"`
	Signer              *string `json:"signer"`
	Issuer              *string `json:"issuer"`
	SigningTime         *string `json:"signing_time"`
	Reason              *string `json:"reason"`
	SubFilter           *string `json:"sub_filter"`
	IntegrityValid      bool    `json:"integrity_valid"`
	CertificateTrusted  bool    `json:"certificate_trusted"`
	CoversWholeDocument bool    `json:"covers_whole_document"`
	Error               *string `json:"error"`
}

type SignatureVerification struct {
	Signed     bool              `json:"signed"`
	AllValid   bool              `json:"all_valid"`
	Signatures []SignatureReport `json:"signatures"`
}

type DetectedField struct {
	Page       int        `json:"page"`
	Rect       [4]float64 `json:"rect"`
	Type       string     `json:"type"`
	Kind       string     `json:"kind"`
	Label      *string    `json:"label"`
	Name       string     `json:"name"`
	Confidence float64    `json:"confidence"`
	Source     string     `json:"source"`
}

type FormDetection struct {
	Pages  int             `json:"pages"`
	Count  int             `json:"count"`
	Fields []DetectedField `json:"fields"`
}

func (s *PDFService) PDFA(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "pdfa", file, nil, params, opts)
}

func (s *PDFService) Compare(ctx context.Context, oldFile, newFile FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	body := merge(params, nil)
	for _, side := range []struct {
		prefix string
		f      FileInput
	}{{"old", oldFile}, {"new", newFile}} {
		if side.f == nil {
			return nil, argError("compare requires both an old and a new file")
		}
		b64, u, err := side.f.resolve()
		if err != nil {
			return nil, err
		}
		if u != "" {
			body[side.prefix+"_file_url"] = u
		} else {
			body[side.prefix+"_file_base64"] = b64
		}
	}
	resp, err := s.c.Request(ctx, http.MethodPost, "/v1/pdf/compare", body, opts)
	if err != nil {
		return nil, err
	}
	return toResult(resp)
}

func (s *PDFService) Sign(ctx context.Context, file FileInput, certificate []byte, certificatePassword string, params map[string]any, opts *RequestOptions) (*Result, error) {
	if len(certificate) == 0 {
		return nil, argError("a PKCS#12 certificate is required")
	}
	fields := map[string]any{
		"certificate_base64":   base64.StdEncoding.EncodeToString(certificate),
		"certificate_password": certificatePassword,
	}
	return s.binary(ctx, "sign", file, fields, params, opts)
}

func (s *PDFService) VerifySignatures(ctx context.Context, file FileInput, opts *RequestOptions) (*SignatureVerification, error) {
	resp, err := s.call(ctx, "signatures/verify", file, nil, nil, opts)
	if err != nil {
		return nil, err
	}
	out := &SignatureVerification{}
	return out, decodeData(resp, out)
}

func (s *PDFService) DetectFormFields(ctx context.Context, file FileInput, params map[string]any, opts *RequestOptions) (*Result, error) {
	return s.binary(ctx, "forms/detect", file, nil, params, opts)
}

type AuditService struct{ c *Client }

type AuditEvent struct {
	Seq          int64           `json:"seq"`
	At           string          `json:"at"`
	Action       string          `json:"action"`
	Actor        string          `json:"actor"`
	InputSHA256  string          `json:"input_sha256"`
	OutputSHA256 string          `json:"output_sha256"`
	Details      json.RawMessage `json:"details"`
}

type AuditPage struct {
	Events        []AuditEvent `json:"events"`
	NextBeforeSeq *int64       `json:"next_before_seq"`
}

type AuditListParams struct {
	Limit     int
	BeforeSeq int64
	Action    string
}

type AuditVerification struct {
	EventsChecked int64      `json:"events_checked"`
	Intact        bool       `json:"intact"`
	TamperedSeqs  []int64    `json:"tampered_seqs"`
	MissingSeqs   [][2]int64 `json:"missing_seqs"`
}

func (s *AuditService) List(ctx context.Context, p *AuditListParams) (*AuditPage, error) {
	q := url.Values{}
	if p != nil {
		if p.Limit > 0 {
			q.Set("limit", strconv.Itoa(p.Limit))
		}
		if p.BeforeSeq > 0 {
			q.Set("before_seq", strconv.FormatInt(p.BeforeSeq, 10))
		}
		if p.Action != "" {
			q.Set("action", p.Action)
		}
	}
	path := "/v1/audit"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	out := &AuditPage{}
	return out, s.c.do(ctx, http.MethodGet, path, nil, nil, out)
}

func (s *AuditService) Verify(ctx context.Context) (*AuditVerification, error) {
	out := &AuditVerification{}
	return out, s.c.do(ctx, http.MethodGet, "/v1/audit/verify", nil, nil, out)
}

type WorkflowTriggersService struct{ c *Client }

type CreateTriggerParams struct {
	Name               string `json:"name,omitempty"`
	RequireSignature   bool   `json:"require_signature,omitempty"`
	CallbackURL        string `json:"callback_url,omitempty"`
	RateLimitPerMinute int    `json:"rate_limit_per_minute,omitempty"`
}

type WorkflowTrigger struct {
	TriggerID          string  `json:"trigger_id"`
	WorkflowID         string  `json:"workflow_id"`
	Name               *string `json:"name"`
	TokenPrefix        string  `json:"token_prefix"`
	RequireSignature   bool    `json:"require_signature"`
	CallbackURL        *string `json:"callback_url"`
	RateLimitPerMinute int64   `json:"rate_limit_per_minute"`
	FireCount          int64   `json:"fire_count"`
	LastFiredAt        *string `json:"last_fired_at"`
	CreatedAt          *string `json:"created_at"`
	URL                string  `json:"url,omitempty"`
	Token              string  `json:"token,omitempty"`
	SigningSecret      string  `json:"signing_secret,omitempty"`
}

func triggersPath(workflowID string) string {
	return "/v1/workflows/" + url.PathEscape(workflowID) + "/triggers"
}

func (s *WorkflowTriggersService) Create(ctx context.Context, workflowID string, p CreateTriggerParams, opts *RequestOptions) (*WorkflowTrigger, error) {
	out := &WorkflowTrigger{}
	return out, s.c.do(ctx, http.MethodPost, triggersPath(workflowID), p, opts, out)
}

func (s *WorkflowTriggersService) List(ctx context.Context, workflowID string) ([]WorkflowTrigger, error) {
	var out []WorkflowTrigger
	return out, s.c.do(ctx, http.MethodGet, triggersPath(workflowID), nil, nil, &out)
}

func (s *WorkflowTriggersService) Revoke(ctx context.Context, workflowID, triggerID string) error {
	_, err := s.c.Request(ctx, http.MethodDelete, triggersPath(workflowID)+"/"+url.PathEscape(triggerID), nil, nil)
	return err
}

const (
	TriggerTimestampHeader = "X-Sigli-Timestamp"
	TriggerSignatureHeader = "X-Sigli-Signature"
)

func ComputeTriggerSignature(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
