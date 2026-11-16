package sigli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Error struct {
	Status int

	Code      string
	Message   string
	RequestID string

	Body []byte

	RetryAfter time.Duration

	cause error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("sigli: ")
	b.WriteString(e.Code)
	if e.Status != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" [request_id=")
		b.WriteString(e.RequestID)
		b.WriteString("]")
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.cause }

func newHTTPError(res *http.Response, raw []byte) *Error {
	e := &Error{
		Status:     res.StatusCode,
		RequestID:  res.Header.Get("X-Request-Id"),
		Body:       raw,
		RetryAfter: parseRetryAfter(res.Header.Get("Retry-After")),
	}
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &env) == nil {
		e.Code, e.Message = env.Code, env.Message
	}
	if e.Code == "" {
		e.Code = fmt.Sprintf("http_%d", res.StatusCode)
	}
	if e.Message == "" {
		if t := strings.TrimSpace(string(raw)); t != "" && !strings.HasPrefix(t, "{") {
			e.Message = t
		} else {
			e.Message = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
	}
	return e
}

func argError(msg string) *Error { return &Error{Code: "invalid_argument", Message: msg} }
