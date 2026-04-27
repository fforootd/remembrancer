package actionitems

import (
	"context"
	"time"

	"zora/internal/llm/prompts"
)

const (
	PromptVersion      = prompts.ActionItemsID
	DefaultLimit       = 40
	DefaultCharBudget  = 32_000
	CandidateCharLimit = 4_000

	SourceStatusVerified   = "verified"
	SourceStatusUnverified = "unverified"
)

type Candidate struct {
	ArtifactID      string              `json:"artifact_id"`
	Title           string              `json:"title"`
	Type            string              `json:"type"`
	Class           string              `json:"class,omitempty"`
	EventAt         string              `json:"event_at"`
	CreatedAt       string              `json:"created_at"`
	Score           int                 `json:"score"`
	Signals         []string            `json:"signals"`
	Facts           []CandidateFact     `json:"facts,omitempty"`
	Relations       []CandidateRelation `json:"relations,omitempty"`
	BriefingHistory []CandidateBriefing `json:"briefing_history,omitempty"`
	Threads         []CandidateThread   `json:"threads,omitempty"`
	Evidence        string              `json:"evidence"`
}

type CandidateThread struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

type CandidateFact struct {
	Type       string  `json:"type"`
	TextValue  string  `json:"text_value"`
	Quote      string  `json:"quote,omitempty"`
	Confidence float64 `json:"confidence"`
}

type CandidateRelation struct {
	Type          string  `json:"type"`
	OtherArtifact string  `json:"other_artifact"`
	Reason        string  `json:"reason"`
	Confidence    float64 `json:"confidence"`
}

type CandidateBriefing struct {
	Title     string `json:"title"`
	ItemTitle string `json:"item_title"`
	CreatedAt string `json:"created_at"`
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
