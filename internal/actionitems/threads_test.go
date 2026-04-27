package actionitems

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestCreateRunResolvesThreadID(t *testing.T) {
	database := newActionItemsTestDB(t)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	insertCandidateArtifact(t, database, "art_bill1", "pdf", "Verizon Apr", start.Add(time.Hour), "Verizon statement Apr.", nil)
	insertCandidateArtifact(t, database, "art_bill2", "pdf", "Verizon May", start.Add(2*time.Hour), "Verizon statement May.", nil)
	insertCandidateArtifact(t, database, "art_other", "pdf", "Acme receipt", start.Add(3*time.Hour), "Acme receipt.", nil)

	insertActionThread(t, database, "thr_verizon", "vendor_account", "Verizon · Apr 2026", end)
	insertActionThreadMember(t, database, "thr_verizon", "art_bill1", end)
	insertActionThreadMember(t, database, "thr_verizon", "art_bill2", end)

	run, err := Repository{DB: database}.CreateRun(context.Background(), CreateRunParams{
		PeriodStart: start,
		PeriodEnd:   end,
		Items: []ValidatedItem{
			{
				Category:     "bills_money",
				Title:        "Pay Verizon",
				Summary:      "Two Verizon statements arrived this period.",
				ArtifactIDs:  []string{"art_bill1", "art_bill2"},
				SourceStatus: SourceStatusVerified,
			},
			{
				Category:     "documents_to_file",
				Title:        "File Acme receipt",
				Summary:      "An Acme receipt arrived.",
				ArtifactIDs:  []string{"art_other"},
				SourceStatus: SourceStatusVerified,
			},
		},
		Now: func() time.Time { return end },
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if len(run.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(run.Items))
	}
	var verizonItem, acmeItem RunItem
	for _, item := range run.Items {
		switch item.Title {
		case "Pay Verizon":
			verizonItem = item
		case "File Acme receipt":
			acmeItem = item
		}
	}
	if verizonItem.ThreadID != "thr_verizon" {
		t.Fatalf("Verizon item ThreadID = %q, expected thr_verizon", verizonItem.ThreadID)
	}
	if verizonItem.ThreadTitle == "" || verizonItem.ThreadKind != "vendor_account" {
		t.Fatalf("Verizon item thread metadata = %+v", verizonItem)
	}
	if acmeItem.ThreadID != "" {
		t.Fatalf("Acme item should have no thread, got %q", acmeItem.ThreadID)
	}
}

func insertActionThread(t *testing.T, database *sql.DB, id, kind, title string, now time.Time) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO thread (id, kind, title, summary, date_start, date_end, status, signature_json, created_at, updated_at)
VALUES (?, ?, ?, NULL, NULL, NULL, 'active', '{}', ?, ?)
`, id, kind, title, formatTime(now), formatTime(now)); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
}

func insertActionThreadMember(t *testing.T, database *sql.DB, threadID, artifactID string, now time.Time) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO thread_member (thread_id, artifact_id, score, source, added_at)
VALUES (?, ?, 1.0, 'rule', ?)
`, threadID, artifactID, formatTime(now)); err != nil {
		t.Fatalf("insert thread_member: %v", err)
	}
}
