package actionitems

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"zora/internal/db"
	"zora/internal/pipeline"
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

func TestStructuredScoreRanksPaymentDueAbovePaidReceipt(t *testing.T) {
	due := Candidate{
		Class: pipeline.ClassBillStatement,
		Facts: []CandidateFact{
			{Type: pipeline.FactPaymentStatus, TextValue: "payment_due"},
			{Type: pipeline.FactIsPaymentDue, TextValue: "true"},
			{Type: pipeline.FactAmountDue, TextValue: "$42.18"},
			{Type: pipeline.FactDueDate, TextValue: "may 10"},
		},
	}
	paid := Candidate{
		Class: pipeline.ClassReceiptPurchase,
		Facts: []CandidateFact{
			{Type: pipeline.FactDocumentType, TextValue: "receipt"},
			{Type: pipeline.FactPaymentStatus, TextValue: "paid"},
			{Type: pipeline.FactIsPaymentDue, TextValue: "false"},
			{Type: pipeline.FactAmountPaid, TextValue: "$42.18"},
			{Type: pipeline.FactAmount, TextValue: "$42.18"},
		},
	}

	dueScore, dueSignals := StructuredScore(due)
	paidScore, paidSignals := StructuredScore(paid)
	if dueScore <= paidScore {
		t.Fatalf("due score %d should exceed paid receipt score %d", dueScore, paidScore)
	}
	if !hasSignal(dueSignals, "payment_due") || !hasSignal(dueSignals, "fact_payment_due") {
		t.Fatalf("due signals = %#v", dueSignals)
	}
	if !hasSignal(paidSignals, "payment_paid") || !hasSignal(paidSignals, "payment_not_due") {
		t.Fatalf("paid signals = %#v", paidSignals)
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

func TestSelectorIncludesStructuredFactsAndRelations(t *testing.T) {
	database := newActionItemsTestDB(t)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	insertCandidateArtifact(t, database, "art_bill", "pdf", "Acme statement", start.Add(24*time.Hour), "Monthly statement.", []string{"Monthly statement."})
	insertCandidateArtifact(t, database, "art_related", "pdf", "Old Acme statement", start.Add(-24*time.Hour), "Old monthly statement.", []string{"Old monthly statement."})

	if _, err := database.Exec(`
INSERT INTO evidence (id, artifact_id, kind, quote, char_start, char_end, provenance_json, created_at)
VALUES ('ev_bill', 'art_bill', 'extracted_text', 'Payment is due Friday.', 0, 22, '{}', '2026-04-21T00:00:00Z');
INSERT INTO artifact_classification (artifact_id, class, evidence_id, confidence, source_type, input_hash, created_at, updated_at)
VALUES ('art_bill', 'bill_statement', 'ev_bill', 0.9, 'rule', 'hash', '2026-04-21T00:00:00Z', '2026-04-21T00:00:00Z');
INSERT INTO extracted_fact (id, artifact_id, fact_type, value_json, text_value, evidence_id, quote, confidence, source_type, input_hash, created_at, updated_at)
VALUES ('fact_due', 'art_bill', 'due_date', '{"text":"Friday"}', 'friday', 'ev_bill', 'due Friday', 0.9, 'rule', 'hash', '2026-04-21T00:00:00Z', '2026-04-21T00:00:00Z');
INSERT INTO proposal (id, type, status, source_artifact_id, title, confidence, created_at, updated_at)
VALUES ('prop_rel', 'artifact_relation', 'proposed', 'art_bill', 'related', 0.7, '2026-04-21T00:00:00Z', '2026-04-21T00:00:00Z');
INSERT INTO artifact_relation (id, proposal_id, source_artifact_id, target_artifact_id, relation_type, reason, confidence, status, created_at, updated_at)
VALUES ('rel_bill', 'prop_rel', 'art_bill', 'art_related', 'updates_fact', 'Related amount changed.', 0.7, 'proposed', '2026-04-21T00:00:00Z', '2026-04-21T00:00:00Z');
`); err != nil {
		t.Fatalf("insert structured candidate data: %v", err)
	}

	candidates, err := Selector{DB: database}.Select(context.Background(), start, end)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates")
	}
	if candidates[0].Class != pipeline.ClassBillStatement {
		t.Fatalf("class = %q", candidates[0].Class)
	}
	if len(candidates[0].Facts) != 1 || candidates[0].Facts[0].Type != pipeline.FactDueDate {
		t.Fatalf("facts = %#v", candidates[0].Facts)
	}
	if len(candidates[0].Relations) != 1 || candidates[0].Relations[0].Type != pipeline.RelationUpdatesFact {
		t.Fatalf("relations = %#v", candidates[0].Relations)
	}
	if !hasSignal(candidates[0].Signals, "fact_action") {
		t.Fatalf("signals = %#v", candidates[0].Signals)
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
