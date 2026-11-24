package moderation

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type scriptedChat struct {
	model.ToolCallingChatModel
	replies []*schema.Message
	inputs  [][]*schema.Message
}

func (m *scriptedChat) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *scriptedChat) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, append([]*schema.Message(nil), messages...))
	reply := m.replies[0]
	m.replies = m.replies[1:]
	return reply, nil
}

func TestAgentReadsThreadAndChecksEvidence(t *testing.T) {
	calls := []schema.ToolCall{{ID: "thread-call", Type: "function", Function: schema.FunctionCall{Name: "get_thread_context", Arguments: `{"scope":"parent_chain","limit":2}`}}}
	const proposal = `{"action":"suggest_keep","policy_id":"","confidence":0.9,"evidence_ids":["parent_chain:1"],"reason":"评论引用上文提醒他人"}`
	chat := &scriptedChat{replies: []*schema.Message{schema.AssistantMessage("", calls), schema.AssistantMessage(proposal, nil)}}
	snapshot := Snapshot{Items: []Evidence{{ID: "target:1", Status: "ok", Text: "别信上面的广告"}}}
	reason, decision, err := askAgent(context.Background(), chat, &snapshot, func(_ context.Context, args threadArgs) ([]Evidence, error) {
		if args.Scope != "parent_chain" || args.Limit != 2 {
			t.Fatal("unexpected tool arguments")
		}
		return []Evidence{{ID: "parent_chain:1", Status: "ok", Text: "加群返现"}}, nil
	})
	if err != nil || reason != "" || !strings.Contains(string(decision), "suggest_keep") {
		t.Fatalf("unexpected result %s %s %v", reason, decision, err)
	}
	second := chat.inputs[1]
	if len(second) != 4 || second[3].ToolCallID != "thread-call" || !strings.Contains(second[3].Content, "加群返现") {
		t.Fatal("model did not receive tool result")
	}
}
func TestAgentRejectsInvalidOutputs(t *testing.T) {
	tests := []struct {
		name     string
		snapshot Snapshot
		reply    *schema.Message
		want     string
	}{
		{"cross-thread argument", Snapshot{}, schema.AssistantMessage("", []schema.ToolCall{{ID: "call", Type: "function", Function: schema.FunctionCall{Name: "get_thread_context", Arguments: `{"scope":"neighbors","limit":2,"comment_id":71}`}}}), "invalid_tool_call"},
		{"forged citation", Snapshot{}, schema.AssistantMessage(`{"action":"suggest_fold","policy_id":"SPAM_PROMO","confidence":0.9,"evidence_ids":["target:1","forged:3"],"reason":"营销"}`, nil), "invalid_output"},
		{"missing reply context", Snapshot{NeedsContext: true}, nil, "insufficient_evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.snapshot.Items = []Evidence{{ID: "target:1", Status: "ok"}}
			chat := &scriptedChat{}
			if test.reply != nil {
				chat.replies = []*schema.Message{test.reply}
			}
			got, decision, err := askAgent(context.Background(), chat, &test.snapshot, func(context.Context, threadArgs) ([]Evidence, error) {
				t.Fatal("tool must not execute")
				return nil, nil
			})
			if err != nil || decision != nil || got != test.want {
				t.Fatalf("got %q %s %v, want %q", got, decision, err, test.want)
			}
		})
	}
}

func TestAgentDoesNotExecuteToolsAfterFinalRound(t *testing.T) {
	makeCall := func(id, name, arguments string) *schema.Message {
		return schema.AssistantMessage("", []schema.ToolCall{
			{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: arguments}},
		})
	}
	chat := &scriptedChat{replies: []*schema.Message{
		makeCall("one", "get_policy_clause", `{"policy_id":"SPAM_PROMO"}`),
		makeCall("two", "get_policy_clause", `{"policy_id":"HARASSMENT"}`),
		makeCall("three", "get_thread_context", `{"scope":"neighbors","limit":1}`),
	}}
	snapshot := Snapshot{Items: []Evidence{{ID: "target:1", Status: "ok"}}}
	failure, result, err := askAgent(context.Background(), chat, &snapshot, func(context.Context, threadArgs) ([]Evidence, error) {
		t.Fatal("tool must not execute after final model request")
		return nil, nil
	})
	if len(snapshot.Tools) != 3 || snapshot.Tools[2].Status != "blocked" {
		t.Fatalf("missing blocked tool trace: %+v", snapshot.Tools)
	}
	if failure != "tool_budget_exhausted" || result != nil || err != nil || len(chat.inputs) != 3 {
		t.Fatalf("unexpected budget result: %q %s %v", failure, result, err)
	}
}

type noToolChat struct{ *scriptedChat }

func (m *noToolChat) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	panic("single-pass evaluation must not bind tools")
}

func TestSinglePassDoesNotBindTools(t *testing.T) {
	chat := &noToolChat{&scriptedChat{replies: []*schema.Message{
		schema.AssistantMessage(`{"action":"suggest_keep","policy_id":"","confidence":0.9,"evidence_ids":["target:1"],"reason":"正常讨论"}`, nil),
	}}}
	snapshot := Snapshot{Items: []Evidence{{ID: "target:1", Status: "ok", Text: "今天风景很好"}}}
	reason, _, err := runAgent(context.Background(), chat, &snapshot, nil, false)
	if err != nil || reason != "" || len(chat.inputs) != 1 {
		t.Fatalf("single-pass failed: reason=%q error=%v calls=%d", reason, err, len(chat.inputs))
	}
}
