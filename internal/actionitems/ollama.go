package actionitems

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"zora/internal/llm/jsonparse"
	"zora/internal/llm/prompts"
)

type OllamaClient struct {
	BaseURL         string
	Model           string
	Timeout         time.Duration
	ContextTokens   int
	MaxOutputTokens int
	Temperature     float64
	HTTPClient      *http.Client
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   any             `json:"format"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaOptions struct {
	NumCtx      int     `json:"num_ctx,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
	Error   string        `json:"error"`
}

func (c OllamaClient) ExtractActionItems(ctx context.Context, req Request) (GeneratedResponse, error) {
	if c.BaseURL == "" {
		return GeneratedResponse{}, fmt.Errorf("ollama base URL is required")
	}
	if c.Model == "" {
		return GeneratedResponse{}, fmt.Errorf("ollama model is required")
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	chatRequest, err := c.chatRequest(req)
	if err != nil {
		return GeneratedResponse{}, err
	}
	body, err := json.Marshal(chatRequest)
	if err != nil {
		return GeneratedResponse{}, fmt.Errorf("encode ollama request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return GeneratedResponse{}, fmt.Errorf("create ollama request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpRequest)
	if err != nil {
		return GeneratedResponse{}, fmt.Errorf("call ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return GeneratedResponse{}, fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var chatResponse ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResponse); err != nil {
		return GeneratedResponse{}, fmt.Errorf("decode ollama response: %w", err)
	}
	if chatResponse.Error != "" {
		return GeneratedResponse{}, fmt.Errorf("ollama error: %s", chatResponse.Error)
	}
	if strings.TrimSpace(chatResponse.Message.Content) == "" {
		return GeneratedResponse{}, fmt.Errorf("ollama returned empty content")
	}

	generated, err := decodeGeneratedResponse(chatResponse.Message.Content)
	if err != nil {
		return GeneratedResponse{}, fmt.Errorf("decode action item JSON: %w", err)
	}
	return generated, nil
}

// decodeGeneratedResponse accepts the shapes models actually produce: the
// requested {"items":[...]} object, a bare [...] array, or {<other-key>:[...]}
// nested under a different but recognisable wrapper key (actions, results,
// data, …). Anything else surfaces the original object-decode error.
func decodeGeneratedResponse(content string) (GeneratedResponse, error) {
	var generated GeneratedResponse
	objErr := jsonparse.UnmarshalLenient([]byte(content), &generated)
	if objErr == nil && len(generated.Items) > 0 {
		return generated, nil
	}
	var items []GeneratedItem
	if arrErr := jsonparse.UnmarshalLenient([]byte(content), &items); arrErr == nil {
		return GeneratedResponse{Items: items}, nil
	}
	var wrapped map[string]json.RawMessage
	if wrapErr := jsonparse.UnmarshalLenient([]byte(content), &wrapped); wrapErr == nil {
		for _, key := range []string{"actions", "action_items", "results", "data", "Items"} {
			raw, ok := wrapped[key]
			if !ok {
				continue
			}
			if err := json.Unmarshal(raw, &items); err == nil && len(items) > 0 {
				return GeneratedResponse{Items: items}, nil
			}
		}
	}
	if objErr == nil {
		return generated, nil
	}
	return GeneratedResponse{}, objErr
}

func (c OllamaClient) chatRequest(req Request) (ollamaChatRequest, error) {
	spec := prompts.ActionItems()
	if req.PromptVersion == "" {
		req.PromptVersion = spec.ID
	}
	candidateJSON, err := json.MarshalIndent(req.Candidates, "", "  ")
	if err != nil {
		return ollamaChatRequest{}, fmt.Errorf("encode action item candidates: %w", err)
	}
	userContent, err := spec.RenderUser(map[string]string{
		"PromptID":       req.PromptVersion,
		"PeriodStart":    formatTime(req.PeriodStart),
		"PeriodEnd":      formatTime(req.PeriodEnd),
		"CandidatesJSON": string(candidateJSON),
	})
	if err != nil {
		return ollamaChatRequest{}, err
	}

	return ollamaChatRequest{
		Model: c.Model,
		Messages: []ollamaMessage{
			{
				Role:    "system",
				Content: spec.System,
			},
			{
				Role:    "user",
				Content: userContent,
			},
		},
		Stream: false,
		Format: spec.Schema,
		Options: ollamaOptions{
			NumCtx:      c.ContextTokens,
			NumPredict:  c.MaxOutputTokens,
			Temperature: c.Temperature,
		},
	}, nil
}

func ActionItemSchema() map[string]any {
	return prompts.ActionItemsSchema()
}
