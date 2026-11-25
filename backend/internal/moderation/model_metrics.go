package moderation

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// measuredChat tracks provider usage in both Worker runs and offline evaluation.
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
