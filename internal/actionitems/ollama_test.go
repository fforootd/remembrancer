package actionitems

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOllamaClientRequestsStructuredChat(t *testing.T) {
	var got ollamaChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollamaChatResponse{
			Message: ollamaMessage{
				Role:    "assistant",
				Content: `{"items":[{"category":"needs_action","title":"Return form","summary":"Return the school form.","why_it_matters":"It has a due date.","action_text":"Return the form.","artifact_ids":["art_1"],"evidence_snippets":[{"artifact_id":"art_1","quote":"return the school form"}],"due_at":"2026-05-10","confidence":0.8}]}`,
			},
			Done: true,
		})
	}))
	defer server.Close()

	client := OllamaClient{
		BaseURL:         server.URL,
		Model:           "gemma4:e2b-it-q4_K_M",
		ContextTokens:   8192,
		MaxOutputTokens: 1024,
		Temperature:     0.1,
	}
	response, err := client.ExtractActionItems(context.Background(), Request{
		PeriodStart: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC),
		Candidates: []Candidate{{
			ArtifactID: "art_1",
			Title:      "School form",
			Type:       "pdf",
			Evidence:   "Please return the school form.",
		}},
	})
	if err != nil {
		t.Fatalf("ExtractActionItems: %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if got.Model != "gemma4:e2b-it-q4_K_M" || got.Stream {
		t.Fatalf("request model/stream = %q/%v", got.Model, got.Stream)
	}
	if got.Options.NumCtx != 8192 || got.Options.NumPredict != 1024 || got.Options.Temperature != 0.1 {
		t.Fatalf("options = %#v", got.Options)
	}
	if got.Format == nil {
		t.Fatal("expected structured output format")
	}
}

func TestOllamaClientReturnsNon200Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model missing", http.StatusNotFound)
	}))
	defer server.Close()

	client := OllamaClient{BaseURL: server.URL, Model: "missing"}
	if _, err := client.ExtractActionItems(context.Background(), Request{}); err == nil {
		t.Fatal("expected non-200 error")
	}
}
