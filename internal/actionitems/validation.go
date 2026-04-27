package actionitems

import (
	"strings"
)

var allowedCategories = map[string]bool{
	"needs_action":      true,
	"bills_money":       true,
	"school_family":     true,
	"travel_events":     true,
	"house_car":         true,
	"documents_to_file": true,
	"interesting":       true,
	"unverified":        true,
}

func ValidateGenerated(response GeneratedResponse, candidates []Candidate) []ValidatedItem {
	candidateEvidence := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		candidateEvidence[candidate.ArtifactID] = normalizeEvidence(candidate.Evidence)
	}

	var out []ValidatedItem
	for _, generated := range response.Items {
		category := strings.TrimSpace(generated.Category)
		actionText := strings.TrimSpace(generated.ActionText)
		if category == "" || !allowedCategories[category] || actionText == "" {
			continue
		}

		artifactIDs, ok := validArtifactIDs(generated.ArtifactIDs, candidateEvidence)
		if !ok {
			continue
		}
		itemArtifacts := make(map[string]bool, len(artifactIDs))
		for _, id := range artifactIDs {
			itemArtifacts[id] = true
		}

		sourceStatus := SourceStatusUnverified
		anyQuote := false
		allQuotesMatched := true
		invalidEvidence := false
		var snippets []EvidenceSnippet
		for _, snippet := range generated.EvidenceSnippets {
			artifactID := strings.TrimSpace(snippet.ArtifactID)
			quote := strings.TrimSpace(snippet.Quote)
			if artifactID == "" || !itemArtifacts[artifactID] {
				invalidEvidence = true
				break
			}
			if _, exists := candidateEvidence[artifactID]; !exists {
				invalidEvidence = true
				break
			}
			if quote == "" {
				continue
			}
			anyQuote = true
			if !strings.Contains(candidateEvidence[artifactID], normalizeEvidence(quote)) {
				allQuotesMatched = false
			}
			snippets = append(snippets, EvidenceSnippet{ArtifactID: artifactID, Quote: quote})
		}
		if invalidEvidence {
			continue
		}
		if !allQuotesMatched && len(generated.EvidenceSnippets) > 0 {
			sourceStatus = SourceStatusUnverified
		} else if anyQuote {
			sourceStatus = SourceStatusVerified
		}

		title := strings.TrimSpace(generated.Title)
		if title == "" {
			title = actionText
		}
		summary := strings.TrimSpace(generated.Summary)
		if summary == "" {
			summary = actionText
		}

		out = append(out, ValidatedItem{
			Category:         category,
			Title:            title,
			Summary:          summary,
			WhyItMatters:     strings.TrimSpace(generated.WhyItMatters),
			ActionText:       actionText,
			ArtifactIDs:      artifactIDs,
			EvidenceSnippets: snippets,
			DueAt:            strings.TrimSpace(generated.DueAt),
			Confidence:       generated.Confidence,
			SourceStatus:     sourceStatus,
			SortOrder:        len(out),
		})
	}
	return out
}

func validArtifactIDs(ids []string, candidateEvidence map[string]string) ([]string, bool) {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, ok := candidateEvidence[id]; !ok {
			return nil, false
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, len(out) > 0
}

func normalizeEvidence(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}
