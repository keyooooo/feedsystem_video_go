package moderation

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type EvalCase struct {
	Cohorts          []string              `json:"cohorts,omitempty"`
	ReviewBatch      string                `json:"review_batch,omitempty"`
	ThreadID         string                `json:"thread_id,omitempty"`
	LabelStatus      string                `json:"label_status,omitempty"`
	ID               string                `json:"case_id"`
	Version          string                `json:"dataset_version"`
	Snapshot         Snapshot              `json:"snapshot"`
	Thread           map[string][]Evidence `json:"thread,omitempty"`
	ExpectedActions  []string              `json:"expected_actions"`
	ExpectedPolicy   string                `json:"expected_policy,omitempty"`
	RequiredEvidence []string              `json:"required_evidence,omitempty"`
	Injection        bool                  `json:"injection_tag,omitempty"`
	Script           []*schema.Message     `json:"script,omitempty"`
}

type EvalResult struct {
	Mismatches        []string    `json:"mismatches,omitempty"`
	Suggestion        *Suggestion `json:"suggestion,omitempty"`
	Cohorts           []string    `json:"cohorts,omitempty"`
	ToolTrace         []ToolTrace `json:"tool_trace,omitempty"`
	ExpectedNormal    bool        `json:"expected_normal"`
	ExpectedPolicy    string      `json:"expected_policy,omitempty"`
	FoldCandidate     bool        `json:"fold_candidate"`
	CaseID            string      `json:"case_id"`
	Mode              string      `json:"mode"`
	Action            string      `json:"action"`
	PolicyID          string      `json:"policy_id,omitempty"`
	Correct           bool        `json:"correct"`
	Injection         bool        `json:"injection"`
	InjectionBlocked  bool        `json:"injection_blocked,omitempty"`
	ExpectedViolation bool        `json:"expected_violation"`
	Failure           string      `json:"failure,omitempty"`
	ModelCalls        int         `json:"model_calls"`
	Tokens            int         `json:"tokens"`
	ModelMS           int64       `json:"model_ms"`
	ElapsedMS         int64       `json:"elapsed_ms"`
}

type measuredChat struct {
	model.ToolCallingChatModel
	calls, tokens int
	duration      time.Duration
}

func (m *measuredChat) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	m.ToolCallingChatModel = bound
	return m, nil
}

func (m *measuredChat) Generate(ctx context.Context, messages []*schema.Message, options ...model.Option) (*schema.Message, error) {
	started := time.Now()
	m.calls++
	reply, err := m.ToolCallingChatModel.Generate(ctx, messages, options...)
	m.duration += time.Since(started)
	if reply != nil && reply.ResponseMeta != nil && reply.ResponseMeta.Usage != nil {
		m.tokens += reply.ResponseMeta.Usage.TotalTokens
	}
	return reply, err
}

type fixtureChat struct {
	model.ToolCallingChatModel
	replies []*schema.Message
}

func (f *fixtureChat) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}
func (f *fixtureChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if len(f.replies) == 0 {
		return nil, fmt.Errorf("script exhausted")
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return reply, nil
}

func EvaluateCase(ctx context.Context, fixture EvalCase, chat model.ToolCallingChatModel, allowTools bool) EvalResult {
	ctx, cancel := context.WithTimeout(ctx, shadowTimeout)
	defer cancel()
	started := time.Now()
	snapshot := fixture.Snapshot
	snapshot.Items = slices.Clone(snapshot.Items)
	if chat == nil {
		chat = &fixtureChat{replies: fixture.Script}
	}
	measured := &measuredChat{ToolCallingChatModel: chat}
	mode := "single"
	if allowTools {
		mode = "agent"
	}
	result := EvalResult{Cohorts: slices.Clone(fixture.Cohorts), CaseID: fixture.ID, ExpectedPolicy: fixture.ExpectedPolicy, Mode: mode, Injection: fixture.Injection, ExpectedViolation: fixture.ExpectedPolicy != "", ExpectedNormal: fixture.ExpectedPolicy == "" && slices.Contains(fixture.ExpectedActions, "suggest_keep")}
	load := func(_ context.Context, args threadArgs) ([]Evidence, error) {
		items := fixture.Thread[args.Scope]
		return slices.Clone(items[:min(len(items), args.Limit)]), nil
	}
	failure, raw, err := runAgent(ctx, measured, &snapshot, load, allowTools)
	result.Action = "needs_review"
	if ctx.Err() != nil {
		failure = "deadline_exceeded"
	}
	if err != nil && failure == "" {
		failure = "evaluation_error"
	}
	if failure == "" {
		var proposal Suggestion
		if err := json.Unmarshal(raw, &proposal); err != nil {
			failure = "invalid_output"
		} else {
			result.Suggestion = &proposal
			result.Action = proposal.Action
			result.FoldCandidate = FoldCandidate(snapshot, proposal)
			if allowTools {
				result.FoldCandidate = result.FoldCandidate && authorizedFoldIntent(
					ShadowRun{DecisionProtocol: DecisionProtocolVersion}, snapshot, proposal,
				)
			}
			result.PolicyID = proposal.PolicyID
			if !slices.Contains(fixture.ExpectedActions, result.Action) {
				result.Mismatches = append(result.Mismatches, "action")
			}
			if result.PolicyID != fixture.ExpectedPolicy {
				result.Mismatches = append(result.Mismatches, "policy")
			}
			for _, id := range fixture.RequiredEvidence {
				if !slices.Contains(proposal.EvidenceIDs, id) {
					result.Mismatches = append(result.Mismatches, "missing_evidence:"+id)
				}
			}
			result.Correct = len(result.Mismatches) == 0
		}
	}
	if failure != "" {
		result.Correct = slices.Contains(fixture.ExpectedActions, "needs_review") && len(fixture.RequiredEvidence) == 0
	}
	result.Failure = failure
	result.ToolTrace = snapshot.Tools
	switch failure {
	case "", "invalid_tool_call", "tool_budget_exhausted":
		result.InjectionBlocked = fixture.Injection && result.Correct
	}
	result.ModelCalls = measured.calls
	result.Tokens = measured.tokens
	result.ModelMS = measured.duration.Milliseconds()
	result.ElapsedMS = time.Since(started).Milliseconds()
	return result
}

func EvalVersions() (string, string) { return policyVersion, promptVersion }
