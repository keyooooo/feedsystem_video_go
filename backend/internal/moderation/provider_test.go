package moderation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/qwen"
	"github.com/cloudwego/eino/schema"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestQwenToolCallingProtocol(t *testing.T) {
	const callID = "call_parent"
	type message struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			ID string `json:"id"`
		} `json:"tool_calls"`
	}
	rounds := 0
	client := &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		rounds++
		if req.Method != http.MethodPost || req.URL.Path != "/compatible-mode/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected provider request: %s %s", req.Method, req.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []message `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		if body.Model != "test-qwen" || len(body.Tools) != 3 || body.Tools[0].Function.Name != "get_thread_context" || body.Tools[1].Function.Name != "get_policy_clause" || body.Tools[2].Function.Name != "submit_review_decision" {
			t.Error("provider did not receive the configured model and decision tools")
		}
		var response string
		switch rounds {
		case 1:
			if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" || !strings.Contains(body.Messages[1].Content, "这条广告别信") {
				t.Error("evidence was not sent as user data")
			}
			response = `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call_parent","type":"function","function":{"name":"get_thread_context","arguments":"{\"scope\":\"parent_chain\",\"limit\":1}"}}]}}]}`
		case 2:
			if len(body.Messages) != 4 || len(body.Messages[2].ToolCalls) != 1 || body.Messages[2].ToolCalls[0].ID != callID ||
				body.Messages[3].Role != "tool" || body.Messages[3].ToolCallID != callID || !strings.Contains(body.Messages[3].Content, "加群返现") {
				t.Error("tool result was not attached to the original call")
			}
			response = `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"{\"action\":\"suggest_keep\",\"policy_id\":\"\",\"confidence\":0.97,\"evidence_ids\":[\"parent_chain:1\"],\"reason\":\"引用广告进行劝阻\"}"}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`
		default:
			t.Error("unexpected extra model request")
			response = `{"choices":[]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	chat, err := qwen.NewChatModel(context.Background(), &qwen.ChatModelConfig{
		APIKey: "test-key", BaseURL: "https://provider.invalid/compatible-mode/v1", Model: "test-qwen", HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	test := EvalCase{
		ID: "warning", ExpectedActions: []string{"suggest_keep"}, RequiredEvidence: []string{"parent_chain:1"},
		Snapshot: Snapshot{Items: []Evidence{{ID: "target:1", Status: "ok", Text: "这条广告别信"}}},
		Thread:   map[string][]Evidence{"parent_chain": {{ID: "parent_chain:1", Status: "ok", Text: "加群返现"}}},
	}
	result := EvaluateCase(context.Background(), test, chat, true)
	if len(result.ToolTrace) != 1 || result.ToolTrace[0].Status != "executed" ||
		len(result.ToolTrace[0].EvidenceIDs) != 1 || result.ToolTrace[0].EvidenceIDs[0] != "parent_chain:1" {
		t.Fatalf("unexpected tool audit: %+v", result.ToolTrace)
	}
	if !result.Correct || result.ModelCalls != 2 || result.Tokens != 120 || rounds != 2 {
		t.Fatalf("unexpected evaluation: %+v", result)
	}
}

func TestQwenRetryClassification(t *testing.T) {
	for _, test := range []struct {
		status int
		retry  bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusUnauthorized, false},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"error":{"message":"provider response","type":"test"}}`))}, nil
			})}
			chat, err := qwen.NewChatModel(context.Background(), &qwen.ChatModelConfig{APIKey: "test-key", BaseURL: "https://provider.invalid", Model: "test-qwen", HTTPClient: client})
			if err != nil {
				t.Fatal(err)
			}
			_, err = chat.Generate(context.Background(), []*schema.Message{schema.UserMessage("test")})
			if err == nil || retryableProviderError(err) != test.retry {
				t.Fatalf("status %d retry=%t: %v", test.status, retryableProviderError(err), err)
			}
		})
	}
}

func TestModelFromEnvArkConfiguration(t *testing.T) {
	t.Setenv("MODERATION_MODEL", "deepseek-v4-1-flash-economy-261017")
	t.Setenv("MODERATION_MODEL_BASE_URL", "https://ark.cn-beijing.volces.com/api/v3")
	t.Setenv("MODERATION_API_KEY", "test-ark-key")
	t.Setenv("DASHSCOPE_API_KEY", "")
	chat, id, err := ModelFromEnv(context.Background())
	if err != nil || chat == nil || id != "deepseek-v4-1-flash-economy-261017" {
		t.Fatalf("Ark configuration failed: model=%q error=%v", id, err)
	}
}
