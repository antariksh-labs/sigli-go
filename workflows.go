package sigli

import (
	"context"
	"net/http"
	"net/url"
)

type WorkflowsService struct {
	c        *Client
	Triggers *WorkflowTriggersService
}

type InputFrom struct {
	Source    string `json:"source"`
	StepIndex *int   `json:"step_index,omitempty"`
}

func FromTrigger() InputFrom { return InputFrom{Source: "trigger"} }

func FromStep(i int) InputFrom { return InputFrom{Source: "step", StepIndex: &i} }

type WorkflowStep struct {
	Op        string         `json:"op"`
	OpKind    string         `json:"op_kind"`
	InputFrom InputFrom      `json:"input_from"`
	Params    map[string]any `json:"params,omitempty"`
}

type CreateWorkflowParams struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Steps       []WorkflowStep `json:"steps"`
}

type Workflow struct {
	WorkflowID  string         `json:"workflow_id"`
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	Version     int64          `json:"version"`
	StepCount   int            `json:"step_count"`
	IsActive    bool           `json:"is_active"`
	CreatedAt   *string        `json:"created_at"`
	UpdatedAt   *string        `json:"updated_at"`
	Steps       []WorkflowStep `json:"steps,omitempty"`
}

type WorkflowRunStep struct {
	StepIndex      int    `json:"step_index"`
	Op             string `json:"op"`
	CreditsCharged int64  `json:"credits_charged"`
	ProcessingMS   int64  `json:"processing_ms"`
}

type WorkflowRun struct {
	RunID               string            `json:"run_id"`
	WorkflowID          *string           `json:"workflow_id"`
	Status              string            `json:"status"`
	Steps               []WorkflowRunStep `json:"steps"`
	TotalCreditsCharged int64             `json:"total_credits_charged"`
	FinalOutput         map[string]any    `json:"final_output"`
	Error               *string           `json:"error"`
}

type WorkflowRunSummary struct {
	RunID               string `json:"run_id"`
	Status              string `json:"status"`
	TotalCreditsCharged int64  `json:"total_credits_charged"`
	CreatedAt           string `json:"created_at"`
}

type RunOptions struct {
	Webhook        string
	Tag            string
	IdempotencyKey string
}

func (s *WorkflowsService) List(ctx context.Context) ([]Workflow, error) {
	var out []Workflow
	return out, s.c.do(ctx, http.MethodGet, "/v1/workflows", nil, nil, &out)
}

func (s *WorkflowsService) Get(ctx context.Context, id string) (*Workflow, error) {
	out := &Workflow{}
	return out, s.c.do(ctx, http.MethodGet, "/v1/workflows/"+url.PathEscape(id), nil, nil, out)
}

func (s *WorkflowsService) Create(ctx context.Context, p CreateWorkflowParams, opts *RequestOptions) (*Workflow, error) {
	out := &Workflow{}
	return out, s.c.do(ctx, http.MethodPost, "/v1/workflows", p, opts, out)
}

func (s *WorkflowsService) Run(ctx context.Context, id string, input map[string]any, o *RunOptions) (*WorkflowRun, error) {
	in, err := withParams(input)
	if err != nil {
		return nil, err
	}
	if o == nil {
		o = &RunOptions{}
	}
	body := map[string]any{"input": in}
	if o.Webhook != "" {
		body["webhook"] = o.Webhook
	}
	if o.Tag != "" {
		body["tag"] = o.Tag
	}
	out := &WorkflowRun{}
	return out, s.c.do(ctx, http.MethodPost, "/v1/workflows/"+url.PathEscape(id)+"/run", body, &RequestOptions{IdempotencyKey: o.IdempotencyKey}, out)
}

func (s *WorkflowsService) Runs(ctx context.Context, id string) ([]WorkflowRunSummary, error) {
	var out []WorkflowRunSummary
	return out, s.c.do(ctx, http.MethodGet, "/v1/workflows/"+url.PathEscape(id)+"/runs", nil, nil, &out)
}
