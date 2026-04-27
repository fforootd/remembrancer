package actionitems

import (
	"context"
	"time"
)

const (
	PromptVersion      = "action-items-v1"
	DefaultLimit       = 40
	DefaultCharBudget  = 32_000
	CandidateCharLimit = 4_000

	SourceStatusVerified   = "verified"
	SourceStatusUnverified = "unverified"
)

type Candidate struct {
	ArtifactID string   `json:"artifact_id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	EventAt    string   `json:"event_at"`
	CreatedAt  string   `json:"created_at"`
	Score      int      `json:"score"`
	Signals    []string `json:"signals"`
	Evidence   string   `json:"evidence"`
}

type Request struct {
	PeriodStart   time.Time
	PeriodEnd     time.Time
	PromptVersion string
	Candidates    []Candidate
}

type Reasoner interface {
	ExtractActionItems(ctx context.Context, req Request) (GeneratedResponse, error)
}

type GeneratedResponse struct {
	Items []GeneratedItem `json:"items"`
}

type GeneratedItem struct {
	Category         string            `json:"category"`
	Title            string            `json:"title"`
	Summary          string            `json:"summary"`
	WhyItMatters     string            `json:"why_it_matters"`
	ActionText       string            `json:"action_text"`
	ArtifactIDs      []string          `json:"artifact_ids"`
	EvidenceSnippets []EvidenceSnippet `json:"evidence_snippets"`
	DueAt            string            `json:"due_at"`
	Confidence       float64           `json:"confidence"`
}

type EvidenceSnippet struct {
	ArtifactID string `json:"artifact_id"`
	Quote      string `json:"quote"`
}

type ValidatedItem struct {
	Category         string
	Title            string
	Summary          string
	WhyItMatters     string
	ActionText       string
	ArtifactIDs      []string
	EvidenceSnippets []EvidenceSnippet
	DueAt            string
	Confidence       float64
	SourceStatus     string
	SortOrder        int
}
