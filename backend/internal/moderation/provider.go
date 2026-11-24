package moderation

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/qwen"
	"github.com/cloudwego/eino/components/model"
	openai "github.com/meguminnnnnnnnn/go-openai"
)

func ModelFromEnv(ctx context.Context) (model.ToolCallingChatModel, string, error) {
	modelID := strings.TrimSpace(os.Getenv("MODERATION_MODEL"))
	key := strings.TrimSpace(os.Getenv("MODERATION_API_KEY"))
	defaultEndpoint := "https://ark.cn-beijing.volces.com/api/v3"
	if key == "" {
		key = strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY"))
		defaultEndpoint = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	if modelID == "" || key == "" {
		return nil, "", errors.New("MODERATION_MODEL and MODERATION_API_KEY are required")
	}
	endpoint := strings.TrimSpace(os.Getenv("MODERATION_MODEL_BASE_URL"))
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	thinking := false
	var enableThinking *bool
	if strings.Contains(endpoint, "dashscope.aliyuncs.com/") {
		enableThinking = &thinking
	}
	maxTokens := 900
	chat, err := qwen.NewChatModel(ctx, &qwen.ChatModelConfig{
		Model: modelID, APIKey: key, BaseURL: endpoint, EnableThinking: enableThinking, MaxTokens: &maxTokens, Timeout: 15 * time.Second,
	})
	if err != nil {
		return nil, "", errors.New("model configuration rejected")
	}
	return chat, modelID, nil
}

func retryableProviderError(err error) bool {
	var api *openai.APIError
	var request *openai.RequestError
	switch {
	case errors.As(err, &api):
		return retryableStatus(api.HTTPStatusCode)
	case errors.As(err, &request):
		return retryableStatus(request.HTTPStatusCode)
	default:
		var network net.Error
		return errors.As(err, &network)
	}
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500 && code < 600
}
