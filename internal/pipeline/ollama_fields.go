package pipeline

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

type OllamaFieldClient struct {
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

func (c OllamaFieldClient) ExtractFacts(ctx context.Context, req FieldRequest) (GeneratedFactResponse, error) {
	if c.BaseURL == "" {
		return GeneratedFactResponse{}, fmt.Errorf("ollama base URL is required")
	}
	if c.Model == "" {
		return GeneratedFactResponse{}, fmt.Errorf("ollama model is required")
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	chatRequest, err := c.chatRequest(req)
	if err != nil {
		return GeneratedFactResponse{}, err
	}
	body, err := json.Marshal(chatRequest)
	if err != nil {
		return GeneratedFactResponse{}, fmt.Errorf("encode field extraction request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return GeneratedFactResponse{}, fmt.Errorf("create ollama field request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpRequest)
	if err != nil {
		return GeneratedFactResponse{}, fmt.Errorf("call ollama for fields: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return GeneratedFactResponse{}, fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var chatResponse ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResponse); err != nil {
		return GeneratedFactResponse{}, fmt.Errorf("decode ollama field response: %w", err)
	}
	if chatResponse.Error != "" {
		return GeneratedFactResponse{}, fmt.Errorf("ollama error: %s", chatResponse.Error)
	}
	if strings.TrimSpace(chatResponse.Message.Content) == "" {
		return GeneratedFactResponse{}, fmt.Errorf("ollama returned empty field content")
	}

	generated, err := decodeGeneratedFactResponse(chatResponse.Message.Content)
	if err != nil {
		return GeneratedFactResponse{}, fmt.Errorf("decode field JSON: %w", err)
	}
	return generated, nil
}

// decodeGeneratedFactResponse accepts the requested {"facts":[...]} object,
// a bare [...] array of facts, or {<other-key>:[...]} nested under a
// recognisable wrapper key. Anything else surfaces the original error.
func decodeGeneratedFactResponse(content string) (GeneratedFactResponse, error) {
	var generated GeneratedFactResponse
	objErr := jsonparse.UnmarshalLenient([]byte(content), &generated)
	if objErr == nil && len(generated.Facts) > 0 {
		return generated, nil
	}
	var facts []GeneratedFact
	if arrErr := jsonparse.UnmarshalLenient([]byte(content), &facts); arrErr == nil {
		return GeneratedFactResponse{Facts: facts}, nil
	}
	var wrapped map[string]json.RawMessage
	if wrapErr := jsonparse.UnmarshalLenient([]byte(content), &wrapped); wrapErr == nil {
		for _, key := range []string{"items", "results", "data", "Facts", "extracted_facts"} {
			raw, ok := wrapped[key]
			if !ok {
				continue
			}
			if err := json.Unmarshal(raw, &facts); err == nil && len(facts) > 0 {
				return GeneratedFactResponse{Facts: facts}, nil
			}
		}
	}
	if objErr == nil {
		return generated, nil
	}
	return GeneratedFactResponse{}, objErr
}

func (c OllamaFieldClient) chatRequest(req FieldRequest) (ollamaChatRequest, error) {
	spec := prompts.ArtifactFields()
	if req.PromptVersion == "" {
		req.PromptVersion = spec.ID
	}
	evidenceJSON, err := json.MarshalIndent(fieldEvidence(req.Evidence), "", "  ")
	if err != nil {
		return ollamaChatRequest{}, fmt.Errorf("encode field evidence: %w", err)
	}
	userContent, err := spec.RenderUser(map[string]string{
		"PromptID":     req.PromptVersion,
		"ArtifactID":   req.ArtifactID,
		"ArtifactType": req.ArtifactType,
		"Title":        req.Title,
		"Class":        req.Class,
		"EvidenceJSON": string(evidenceJSON),
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

func fieldEvidence(evidence []Evidence) []map[string]any {
	out := make([]map[string]any, 0, len(evidence))
	for _, item := range evidence {
		out = append(out, map[string]any{
			"evidence_id": item.ID,
			"artifact_id": item.ArtifactID,
			"chunk_id":    item.ChunkID,
			"quote":       item.Quote,
			"char_start":  item.CharStart,
			"char_end":    item.CharEnd,
			"page_start":  item.PageStart,
			"page_end":    item.PageEnd,
		})
	}
	return out
}

func FieldSchema() map[string]any {
	return prompts.ArtifactFieldsSchema()
}
