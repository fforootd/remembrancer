package actionitems

import (
	"context"
	"testing"
	"time"
)

func TestRepositoryPersistsRunItemsAndArtifactLinks(t *testing.T) {
	database := newActionItemsTestDB(t)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	insertCandidateArtifact(t, database, "art_form", "pdf", "School form", start.Add(time.Hour), "Please return this form by May 10.", nil)

	run, err := Repository{DB: database}.CreateRun(context.Background(), CreateRunParams{
		PeriodStart:     start,
		PeriodEnd:       end,
		SourceQueryJSON: `{"candidate_ids":["art_form"]}`,
		ModelName:       "gemma4:e2b-it-q4_K_M",
		PromptVersion:   PromptVersion,
		Items: []ValidatedItem{{
			Category:     "needs_action",
			Title:        "Return school form",
			Summary:      "A school form needs to be returned.",
			ActionText:   "Return the school form.",
			ArtifactIDs:  []string{"art_form"},
			SourceStatus: SourceStatusVerified,
			EvidenceSnippets: []EvidenceSnippet{{
				ArtifactID: "art_form",
				Quote:      "Please return this form by May 10.",
			}},
		}},
		Now: func() time.Time { return end },
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.ID == "" || len(run.Items) != 1 {
		t.Fatalf("run = %#v", run)
	}
	if len(run.Items[0].Artifacts) != 1 || run.Items[0].Artifacts[0].ID != "art_form" {
		t.Fatalf("artifacts = %#v", run.Items[0].Artifacts)
	}

	got, ok, err := Repository{DB: database}.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if !ok || len(got.Items) != 1 || got.Items[0].Artifacts[0].Snippet == "" {
		t.Fatalf("got = %#v ok=%v", got, ok)
	}
}
