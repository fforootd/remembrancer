package actionitems

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"zora/internal/db"
)

func TestScoreRanksActionableHouseholdDocumentsAboveNewsletters(t *testing.T) {
	billScore, billSignals := Score("Insurance renewal", "pdf", "Your insurance renewal payment is due by May 10. Please pay the balance.")
	newsletterScore, newsletterSignals := Score("Weekly digest", "text", "This newsletter includes a webinar promotion. Unsubscribe or manage preferences.")

	if billScore <= newsletterScore {
		t.Fatalf("bill score %d should be greater than newsletter score %d", billScore, newsletterScore)
	}
	if !hasSignal(billSignals, "action") || !hasSignal(billSignals, "money") {
		t.Fatalf("bill signals = %#v", billSignals)
	}
	if !hasSignal(newsletterSignals, "newsletter") {
		t.Fatalf("newsletter signals = %#v", newsletterSignals)
	}
}

func TestSelectorUsesChunksAndAppliesPromptBudget(t *testing.T) {
	database := newActionItemsTestDB(t)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	longChunk := strings.Repeat("Please submit the school permission form by Friday. ", 200)
	insertCandidateArtifact(t, database, "art_school", "pdf", "School form", start.Add(24*time.Hour), "short fallback", []string{longChunk})
	insertCandidateArtifact(t, database, "art_newsletter", "text", "Weekly digest", start.Add(48*time.Hour), "webinar promotion unsubscribe newsletter", nil)

	candidates, err := Selector{DB: database, EvidenceBudget: 900}.Select(context.Background(), start, end)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
	if candidates[0].ArtifactID != "art_school" {
		t.Fatalf("top candidate = %q", candidates[0].ArtifactID)
	}
	if strings.Contains(candidates[0].Evidence, "short fallback") {
		t.Fatalf("expected chunk evidence, got fallback text: %q", candidates[0].Evidence)
	}
	if len([]rune(candidates[0].Evidence)) > 900 {
		t.Fatalf("evidence length = %d", len([]rune(candidates[0].Evidence)))
	}
}

func TestApplyEvidenceBudgetCapsCandidatePayload(t *testing.T) {
	candidates := []Candidate{
		{ArtifactID: "art_1", Evidence: strings.Repeat("a", 5000)},
		{ArtifactID: "art_2", Evidence: strings.Repeat("b", 5000)},
		{ArtifactID: "art_3", Evidence: strings.Repeat("c", 5000)},
	}
	packed := ApplyEvidenceBudget(candidates, 6500)

	total := 0
	for _, candidate := range packed {
		total += len([]rune(candidate.Evidence))
		if len([]rune(candidate.Evidence)) > CandidateCharLimit {
			t.Fatalf("candidate evidence length = %d", len([]rune(candidate.Evidence)))
		}
	}
	if total > 6500 {
		t.Fatalf("packed evidence total = %d", total)
	}
	if len(packed) != 2 {
		t.Fatalf("packed candidates = %d", len(packed))
	}
}

func hasSignal(signals []string, want string) bool {
	for _, signal := range signals {
		if signal == want {
			return true
		}
	}
	return false
}

func newActionItemsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(t.TempDir() + "/main.sqlite")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return database
}

func insertCandidateArtifact(t *testing.T, database *sql.DB, id, artifactType, title string, eventAt time.Time, text string, chunks []string) {
	t.Helper()
	hash := "hash_" + id
	if _, err := database.Exec(`
INSERT INTO blob (hash, algorithm, size_bytes, storage_path, created_at)
VALUES (?, 'sha256', 3, '/tmp/blob', '2026-04-20T00:00:00Z')
`, hash); err != nil {
		t.Fatalf("insert blob %s: %v", id, err)
	}
	if _, err := database.Exec(`
INSERT INTO artifact (id, type, source, source_id, title, owner, content_hash, captured_at, event_at, created_at)
VALUES (?, ?, 'watch_folder', ?, ?, 'florian', ?, ?, ?, ?)
`,
		id,
		artifactType,
		"source_"+id,
		title,
		hash,
		formatTime(eventAt),
		formatTime(eventAt),
		formatTime(eventAt),
	); err != nil {
		t.Fatalf("insert artifact %s: %v", id, err)
	}
	if _, err := database.Exec(`
INSERT INTO extracted_text (artifact_id, text, extractor, created_at)
VALUES (?, ?, 'test', ?)
`,
		id,
		text,
		formatTime(eventAt),
	); err != nil {
		t.Fatalf("insert artifact %s: %v", id, err)
	}
	for i, chunk := range chunks {
		if _, err := database.Exec(`
INSERT INTO artifact_chunk (id, artifact_id, ordinal, title, text, char_start, char_end, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, "chk_"+id+"_"+string(rune('a'+i)), id, i, title, chunk, i*100, i*100+len(chunk), formatTime(eventAt)); err != nil {
			t.Fatalf("insert chunk %s: %v", id, err)
		}
	}
}
