package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type classRule struct {
	class string
	words []string
}

var classRules = []classRule{
	{ClassNewsletterPromo, []string{"unsubscribe", "newsletter", "promotion", "sale", "webinar", "manage preferences", "view in browser"}},
	{ClassSchoolFamily, []string{"school", "teacher", "permission slip", "parent", "student", "field trip", "classroom", "homework"}},
	{ClassMedicalHealth, []string{"doctor", "dentist", "clinic", "prescription", "medication", "allergy", "diagnosis", "patient", "treatment"}},
	{ClassInsuranceVehicle, []string{"insurance", "policy", "premium", "vehicle", "registration", "vin", "license plate", "coverage"}},
	{ClassTaxFinance, []string{"tax", "w-2", "1099", "irs", "deduction", "filing", "return", "withholding"}},
	{ClassTravelEvent, []string{"flight", "hotel", "reservation", "itinerary", "booking", "passport", "boarding", "trip"}},
	{ClassIdentityLegal, []string{"passport", "driver license", "birth certificate", "ssn", "social security", "legal", "notary"}},
	{ClassBillStatement, []string{"invoice", "bill", "statement", "balance", "amount due", "payment due", "pay by", "renewal"}},
	{ClassReceiptPurchase, []string{"receipt", "subtotal", "total", "paid", "purchase", "refund", "order number"}},
	{ClassCorrespondence, []string{"dear", "hello", "regards", "sincerely", "reply", "contact us"}},
}

type scoredClass struct {
	class string
	score int
}

func ClassifyArtifact(ctx context.Context, db *sql.DB, artifactID string, evidence []Evidence, now time.Time) (Classification, error) {
	snapshot, ok, err := LoadArtifactSnapshot(ctx, db, artifactID)
	if err != nil {
		return Classification{}, err
	}
	if !ok {
		return Classification{}, fmt.Errorf("artifact %s not found", artifactID)
	}

	text := snapshot.Title + "\n" + snapshot.Text
	for _, item := range evidence {
		text += "\n" + item.Quote
	}
	class, confidence := classify(snapshot.Type, text)
	evidenceID := bestEvidenceForClass(class, evidence)
	hash := inputHash(snapshot.ID, snapshot.ContentHash, snapshot.Title, snapshot.Text, class)

	result := Classification{
		ArtifactID: artifactID,
		Class:      class,
		EvidenceID: evidenceID,
		Confidence: confidence,
		SourceType: SourceRule,
		InputHash:  hash,
	}
	if err := StoreClassification(ctx, db, result, now); err != nil {
		return Classification{}, err
	}
	return result, nil
}

func StoreClassification(ctx context.Context, db *sql.DB, classification Classification, now time.Time) error {
	if !allowedClasses[classification.Class] {
		return fmt.Errorf("invalid artifact class %q", classification.Class)
	}
	if classification.Confidence < 0 || classification.Confidence > 1 {
		return fmt.Errorf("classification confidence must be between 0 and 1")
	}
	if classification.SourceType == "" {
		classification.SourceType = SourceRule
	}
	if classification.InputHash == "" {
		classification.InputHash = inputHash(classification.ArtifactID, classification.Class)
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO artifact_classification (
	artifact_id, class, evidence_id, confidence, source_type,
	model_name, prompt_version, input_hash, created_at, updated_at
) VALUES (?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)
ON CONFLICT(artifact_id) DO UPDATE SET
	class = excluded.class,
	evidence_id = excluded.evidence_id,
	confidence = excluded.confidence,
	source_type = excluded.source_type,
	model_name = excluded.model_name,
	prompt_version = excluded.prompt_version,
	input_hash = excluded.input_hash,
	updated_at = excluded.updated_at
`,
		classification.ArtifactID,
		classification.Class,
		classification.EvidenceID,
		classification.Confidence,
		classification.SourceType,
		classification.ModelName,
		classification.PromptVersion,
		classification.InputHash,
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("store artifact classification: %w", err)
	}
	return nil
}

func LoadClassification(ctx context.Context, db *sql.DB, artifactID string) (Classification, bool, error) {
	row := db.QueryRowContext(ctx, `
SELECT artifact_id, class, COALESCE(evidence_id, ''), confidence, source_type,
	COALESCE(model_name, ''), COALESCE(prompt_version, ''), input_hash
FROM artifact_classification
WHERE artifact_id = ?
`, artifactID)
	var classification Classification
	if err := row.Scan(
		&classification.ArtifactID,
		&classification.Class,
		&classification.EvidenceID,
		&classification.Confidence,
		&classification.SourceType,
		&classification.ModelName,
		&classification.PromptVersion,
		&classification.InputHash,
	); err != nil {
		if err == sql.ErrNoRows {
			return Classification{}, false, nil
		}
		return Classification{}, false, fmt.Errorf("load classification: %w", err)
	}
	return classification, true, nil
}

func classify(artifactType, text string) (string, float64) {
	haystack := normalize(text)
	var scored []scoredClass
	for _, rule := range classRules {
		score := 0
		for _, word := range rule.words {
			if strings.Contains(haystack, normalize(word)) {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, scoredClass{class: rule.class, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	if len(scored) > 0 {
		confidence := 0.55 + float64(scored[0].score)*0.1
		if confidence > 0.95 {
			confidence = 0.95
		}
		return scored[0].class, confidence
	}
	if artifactType == "image" && strings.TrimSpace(text) == "" {
		return ClassPhotoMemory, 0.45
	}
	return ClassGenericDocument, 0.4
}

func bestEvidenceForClass(class string, evidence []Evidence) string {
	var words []string
	for _, rule := range classRules {
		if rule.class == class {
			words = rule.words
			break
		}
	}
	for _, item := range evidence {
		quote := normalize(item.Quote)
		for _, word := range words {
			if strings.Contains(quote, normalize(word)) {
				return item.ID
			}
		}
	}
	if len(evidence) > 0 {
		return evidence[0].ID
	}
	return ""
}
