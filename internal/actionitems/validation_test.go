package actionitems

import "testing"

func TestValidateGeneratedVerifiesMatchedQuotes(t *testing.T) {
	candidates := []Candidate{{
		ArtifactID: "art_1",
		Evidence:   "Please return this form by May 10.",
	}}
	response := GeneratedResponse{Items: []GeneratedItem{{
		Category:    "needs_action",
		Title:       "Return form",
		Summary:     "A form needs to be returned.",
		ActionText:  "Return the form.",
		ArtifactIDs: []string{"art_1"},
		EvidenceSnippets: []EvidenceSnippet{{
			ArtifactID: "art_1",
			Quote:      "Please return this form by May 10.",
		}},
		Confidence: 0.9,
	}}}

	items := ValidateGenerated(response, candidates)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].SourceStatus != SourceStatusVerified {
		t.Fatalf("source status = %q", items[0].SourceStatus)
	}
}

func TestValidateGeneratedKeepsUnmatchedQuotesUnverified(t *testing.T) {
	candidates := []Candidate{{
		ArtifactID: "art_1",
		Evidence:   "Please return this form by May 10.",
	}}
	response := GeneratedResponse{Items: []GeneratedItem{{
		Category:    "needs_action",
		Title:       "Return form",
		Summary:     "A form needs to be returned.",
		ActionText:  "Return the form.",
		ArtifactIDs: []string{"art_1"},
		EvidenceSnippets: []EvidenceSnippet{{
			ArtifactID: "art_1",
			Quote:      "Please return this form by June 1.",
		}},
	}}}

	items := ValidateGenerated(response, candidates)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].SourceStatus != SourceStatusUnverified {
		t.Fatalf("source status = %q", items[0].SourceStatus)
	}
}

func TestValidateGeneratedDropsInvalidItems(t *testing.T) {
	candidates := []Candidate{{ArtifactID: "art_1", Evidence: "Please sign."}}
	response := GeneratedResponse{Items: []GeneratedItem{
		{Category: "needs_action", ActionText: "Sign.", ArtifactIDs: nil},
		{Category: "unknown", ActionText: "Sign.", ArtifactIDs: []string{"art_1"}},
		{Category: "needs_action", ActionText: "", ArtifactIDs: []string{"art_1"}},
		{Category: "needs_action", ActionText: "Sign.", ArtifactIDs: []string{"missing"}},
		{
			Category:    "needs_action",
			ActionText:  "Sign.",
			ArtifactIDs: []string{"art_1"},
			EvidenceSnippets: []EvidenceSnippet{{
				ArtifactID: "missing",
				Quote:      "Please sign.",
			}},
		},
	}}

	items := ValidateGenerated(response, candidates)
	if len(items) != 0 {
		t.Fatalf("items = %#v", items)
	}
}
