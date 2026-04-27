package pipeline

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestClusterArtifactCreatesAndJoinsThreads(t *testing.T) {
	database := newPipelineTestDB(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	insertPipelineArtifact(t, database, "art_v1", "hash_v1", "pdf", "Verizon bill April", now.AddDate(0, 0, -7), "Verizon Wireless · April statement · $84.30")
	insertPipelineArtifact(t, database, "art_v2", "hash_v2", "pdf", "Verizon bill May", now.AddDate(0, 0, -1), "Verizon Wireless · May statement · $84.30")
	insertPipelineArtifact(t, database, "art_acme", "hash_acme", "pdf", "Acme receipt", now.AddDate(0, 0, -2), "Acme Hardware · receipt · $19.99")

	stageArtifactWithVendor(t, database, "art_v1", ClassBillStatement, "verizon wireless", now)
	stageArtifactWithVendor(t, database, "art_v2", ClassBillStatement, "verizon wireless", now)
	stageArtifactWithVendor(t, database, "art_acme", ClassReceiptPurchase, "acme hardware", now)

	first, err := ClusterArtifact(context.Background(), database, "art_v1", now)
	if err != nil {
		t.Fatalf("cluster art_v1: %v", err)
	}
	if len(first) != 1 || !first[0].NewThread {
		t.Fatalf("art_v1 assignments = %#v", first)
	}
	threadV := first[0].ThreadID

	second, err := ClusterArtifact(context.Background(), database, "art_v2", now)
	if err != nil {
		t.Fatalf("cluster art_v2: %v", err)
	}
	if len(second) != 1 || second[0].NewThread {
		t.Fatalf("art_v2 should have joined existing thread, got %#v", second)
	}
	if second[0].ThreadID != threadV {
		t.Fatalf("art_v2 joined thread %q, expected %q", second[0].ThreadID, threadV)
	}
	if second[0].Score < threadAutoAddScore {
		t.Fatalf("art_v2 score %.2f below auto-add %.2f", second[0].Score, threadAutoAddScore)
	}

	third, err := ClusterArtifact(context.Background(), database, "art_acme", now)
	if err != nil {
		t.Fatalf("cluster art_acme: %v", err)
	}
	if len(third) != 1 || !third[0].NewThread {
		t.Fatalf("art_acme should have its own thread, got %#v", third)
	}
	if third[0].ThreadID == threadV {
		t.Fatalf("art_acme accidentally joined Verizon thread")
	}

	var members int
	if err := database.QueryRow(`SELECT COUNT(*) FROM thread_member WHERE thread_id = ?`, threadV).Scan(&members); err != nil {
		t.Fatalf("count thread_member: %v", err)
	}
	if members != 2 {
		t.Fatalf("Verizon thread membership = %d", members)
	}

	var title, kind string
	if err := database.QueryRow(`SELECT title, kind FROM thread WHERE id = ?`, threadV).Scan(&title, &kind); err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if kind != ThreadKindVendorAccount {
		t.Fatalf("Verizon thread kind = %q", kind)
	}
	if title == "" {
		t.Fatalf("Verizon thread title is empty")
	}
}

func TestClusterArtifactSkipsNonEligibleClasses(t *testing.T) {
	database := newPipelineTestDB(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	insertPipelineArtifact(t, database, "art_promo", "hash_promo", "email", "Promo", now, "10% off this weekend at Best Buy")
	stageArtifactWithVendor(t, database, "art_promo", ClassNewsletterPromo, "best buy", now)

	out, err := ClusterArtifact(context.Background(), database, "art_promo", now)
	if err != nil {
		t.Fatalf("ClusterArtifact: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no thread for newsletter, got %#v", out)
	}

	var threads int
	if err := database.QueryRow(`SELECT COUNT(*) FROM thread`).Scan(&threads); err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if threads != 0 {
		t.Fatalf("thread count = %d", threads)
	}
}

func TestRunnerAdvancesClusterThreadsStage(t *testing.T) {
	database := newPipelineTestDB(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	insertPipelineArtifact(t, database, "art_school", "hash_school", "pdf", "School permission form", now, "Please return the school permission form by Friday.")

	result, err := (Runner{
		DB:  database,
		Now: func() time.Time { return now },
	}).ProcessArtifact(context.Background(), "art_school")
	if err != nil {
		t.Fatalf("ProcessArtifact: %v", err)
	}
	if result.ThreadCount == 0 {
		t.Fatalf("expected at least one thread assignment, got %#v", result)
	}

	var stageStatus string
	if err := database.QueryRow(`
SELECT status FROM pipeline_stage WHERE artifact_id = ? AND stage = ?
`, "art_school", StageClusterThreads).Scan(&stageStatus); err != nil {
		t.Fatalf("read stage: %v", err)
	}
	if stageStatus != "succeeded" {
		t.Fatalf("cluster_threads stage status = %q", stageStatus)
	}
}

func stageArtifactWithVendor(t *testing.T, database *sql.DB, artifactID, class, vendor string, now time.Time) {
	t.Helper()
	evidence, err := UpsertEvidenceForArtifact(context.Background(), database, artifactID, now)
	if err != nil {
		t.Fatalf("UpsertEvidenceForArtifact %s: %v", artifactID, err)
	}
	if len(evidence) == 0 {
		t.Fatalf("no evidence produced for %s", artifactID)
	}
	if err := StoreClassification(context.Background(), database, Classification{
		ArtifactID: artifactID,
		Class:      class,
		EvidenceID: evidence[0].ID,
		Confidence: 0.9,
		SourceType: SourceRule,
		InputHash:  artifactID + class,
	}, now); err != nil {
		t.Fatalf("StoreClassification %s: %v", artifactID, err)
	}
	if err := ReplaceFacts(context.Background(), database, artifactID, []Fact{
		testFact(artifactID, FactVendor, vendor, evidence[0].ID),
	}, now); err != nil {
		t.Fatalf("ReplaceFacts %s: %v", artifactID, err)
	}
}
