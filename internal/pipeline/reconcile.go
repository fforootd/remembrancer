package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func ReconcileArtifact(ctx context.Context, db *sql.DB, artifactID string, now time.Time) ([]Relation, error) {
	current, ok, err := LoadArtifactSnapshot(ctx, db, artifactID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("artifact %s not found", artifactID)
	}
	classification, _, err := LoadClassification(ctx, db, artifactID)
	if err != nil {
		return nil, err
	}
	facts, err := LoadFacts(ctx, db, artifactID)
	if err != nil {
		return nil, err
	}

	var relations []Relation
	duplicates, err := duplicateRelations(ctx, db, current)
	if err != nil {
		return nil, err
	}
	relations = append(relations, duplicates...)

	matches, err := factMatchRelations(ctx, db, artifactID, classification.Class, facts)
	if err != nil {
		return nil, err
	}
	relations = append(relations, matches...)

	conflicts, err := amountConflictRelations(ctx, db, artifactID, classification.Class, facts)
	if err != nil {
		return nil, err
	}
	relations = append(relations, conflicts...)

	for _, relation := range relations {
		if err := StoreRelationProposal(ctx, db, relation, now); err != nil {
			return nil, err
		}
	}
	return relations, nil
}

func LoadFacts(ctx context.Context, db *sql.DB, artifactID string) ([]Fact, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, artifact_id, fact_type, value_json, text_value, COALESCE(evidence_id, ''),
	COALESCE(quote, ''), confidence, source_type, COALESCE(model_name, ''),
	COALESCE(prompt_version, ''), input_hash
FROM extracted_fact
WHERE artifact_id = ?
ORDER BY fact_type, confidence DESC
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query facts: %w", err)
	}
	defer rows.Close()

	var out []Fact
	for rows.Next() {
		var fact Fact
		if err := rows.Scan(
			&fact.ID,
			&fact.ArtifactID,
			&fact.Type,
			&fact.ValueJSON,
			&fact.TextValue,
			&fact.EvidenceID,
			&fact.Quote,
			&fact.Confidence,
			&fact.SourceType,
			&fact.ModelName,
			&fact.PromptVersion,
			&fact.InputHash,
		); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		out = append(out, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate facts: %w", err)
	}
	return out, nil
}

func StoreRelationProposal(ctx context.Context, db *sql.DB, relation Relation, now time.Time) error {
	if !allowedRelationTypes[relation.Type] {
		return fmt.Errorf("invalid relation type %q", relation.Type)
	}
	if relation.SourceArtifactID == "" || relation.TargetArtifactID == "" {
		return fmt.Errorf("relation artifacts are required")
	}
	if relation.SourceArtifactID == relation.TargetArtifactID {
		return fmt.Errorf("relation artifacts must differ")
	}
	if relation.Status == "" {
		relation.Status = StatusProposed
	}
	if relation.Confidence < 0 || relation.Confidence > 1 {
		return fmt.Errorf("relation confidence must be between 0 and 1")
	}
	if relation.Reason == "" {
		relation.Reason = relation.Type
	}
	if relation.ID == "" {
		relation.ID = hashID("rel", relation.SourceArtifactID, relation.TargetArtifactID, relation.Type, relation.Reason)
	}
	if relation.ProposalID == "" {
		relation.ProposalID = hashID("prop", relation.ID)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin relation proposal: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO proposal (
	id, type, status, source_artifact_id, title, summary, confidence, created_at, updated_at
) VALUES (?, 'artifact_relation', 'proposed', ?, ?, NULLIF(?, ''), ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	status = proposal.status,
	title = excluded.title,
	summary = excluded.summary,
	confidence = excluded.confidence,
	updated_at = excluded.updated_at
`,
		relation.ProposalID,
		relation.SourceArtifactID,
		relationTitle(relation),
		relation.Reason,
		relation.Confidence,
		formatTime(now),
		formatTime(now),
	); err != nil {
		return fmt.Errorf("insert relation proposal: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO artifact_relation (
	id, proposal_id, source_artifact_id, target_artifact_id, relation_type,
	source_evidence_id, target_evidence_id, reason, confidence, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	reason = excluded.reason,
	confidence = excluded.confidence,
	status = CASE
		WHEN artifact_relation.status = 'accepted' THEN artifact_relation.status
		ELSE excluded.status
	END,
	updated_at = excluded.updated_at
`,
		relation.ID,
		relation.ProposalID,
		relation.SourceArtifactID,
		relation.TargetArtifactID,
		relation.Type,
		relation.SourceEvidenceID,
		relation.TargetEvidenceID,
		relation.Reason,
		relation.Confidence,
		relation.Status,
		formatTime(now),
		formatTime(now),
	); err != nil {
		return fmt.Errorf("insert artifact relation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit relation proposal: %w", err)
	}
	return nil
}

func duplicateRelations(ctx context.Context, db *sql.DB, current artifactSnapshot) ([]Relation, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id
FROM artifact
WHERE content_hash = ?
	AND id <> ?
	AND deleted_at IS NULL
ORDER BY event_at DESC, created_at DESC
LIMIT 8
`, current.ContentHash, current.ID)
	if err != nil {
		return nil, fmt.Errorf("query duplicate artifacts: %w", err)
	}
	defer rows.Close()

	var out []Relation
	for rows.Next() {
		var targetID string
		if err := rows.Scan(&targetID); err != nil {
			return nil, fmt.Errorf("scan duplicate artifact: %w", err)
		}
		out = append(out, Relation{
			ID:               hashID("rel", current.ID, targetID, RelationDuplicateOf),
			ProposalID:       hashID("prop", current.ID, targetID, RelationDuplicateOf),
			SourceArtifactID: current.ID,
			TargetArtifactID: targetID,
			Type:             RelationDuplicateOf,
			Reason:           "Artifacts share the same content hash.",
			Confidence:       0.99,
			Status:           StatusProposed,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate duplicate artifacts: %w", err)
	}
	return out, nil
}

func factMatchRelations(ctx context.Context, db *sql.DB, artifactID, class string, facts []Fact) ([]Relation, error) {
	var out []Relation
	for _, fact := range facts {
		relationType, ok := relationForExactFact(fact.Type)
		if !ok || fact.TextValue == "" {
			continue
		}
		rows, err := db.QueryContext(ctx, `
SELECT f.artifact_id, COALESCE(f.evidence_id, '')
FROM extracted_fact f
LEFT JOIN artifact_classification c ON c.artifact_id = f.artifact_id
WHERE f.artifact_id <> ?
	AND f.fact_type = ?
	AND f.text_value = ?
	AND (? = '' OR c.class IS NULL OR c.class = ?)
ORDER BY f.updated_at DESC
LIMIT 12
`, artifactID, fact.Type, fact.TextValue, class, class)
		if err != nil {
			return nil, fmt.Errorf("query fact matches: %w", err)
		}
		for rows.Next() {
			var targetID, targetEvidenceID string
			if err := rows.Scan(&targetID, &targetEvidenceID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan fact match: %w", err)
			}
			out = append(out, Relation{
				ID:               hashID("rel", artifactID, targetID, relationType, fact.Type, fact.TextValue),
				ProposalID:       hashID("prop", artifactID, targetID, relationType, fact.Type, fact.TextValue),
				SourceArtifactID: artifactID,
				TargetArtifactID: targetID,
				Type:             relationType,
				SourceEvidenceID: fact.EvidenceID,
				TargetEvidenceID: targetEvidenceID,
				Reason:           fmt.Sprintf("Both artifacts mention %s: %s.", fact.Type, fact.TextValue),
				Confidence:       0.72,
				Status:           StatusProposed,
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate fact matches: %w", err)
		}
		rows.Close()
	}
	return dedupeRelations(out), nil
}

func amountConflictRelations(ctx context.Context, db *sql.DB, artifactID, class string, facts []Fact) ([]Relation, error) {
	currentAmounts := factsByType(facts, FactAmount)
	if len(currentAmounts) == 0 {
		return nil, nil
	}
	keys := relationKeyFacts(facts)
	if len(keys) == 0 {
		return nil, nil
	}

	var out []Relation
	for _, amount := range currentAmounts {
		rows, err := db.QueryContext(ctx, `
SELECT amount.artifact_id, COALESCE(amount.evidence_id, ''), amount.text_value
FROM extracted_fact amount
LEFT JOIN artifact_classification c ON c.artifact_id = amount.artifact_id
WHERE amount.artifact_id <> ?
	AND amount.fact_type = 'amount'
	AND amount.text_value <> ?
	AND (? = '' OR c.class IS NULL OR c.class = ?)
ORDER BY amount.updated_at DESC
LIMIT 24
`, artifactID, amount.TextValue, class, class)
		if err != nil {
			return nil, fmt.Errorf("query amount conflicts: %w", err)
		}
		for rows.Next() {
			var targetID, targetEvidenceID, targetAmount string
			if err := rows.Scan(&targetID, &targetEvidenceID, &targetAmount); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan amount conflict: %w", err)
			}
			if !sharesFactKey(ctx, db, targetID, keys) {
				continue
			}
			out = append(out, Relation{
				ID:               hashID("rel", artifactID, targetID, RelationUpdatesFact, amount.TextValue, targetAmount),
				ProposalID:       hashID("prop", artifactID, targetID, RelationUpdatesFact, amount.TextValue, targetAmount),
				SourceArtifactID: artifactID,
				TargetArtifactID: targetID,
				Type:             RelationUpdatesFact,
				SourceEvidenceID: amount.EvidenceID,
				TargetEvidenceID: targetEvidenceID,
				Reason:           fmt.Sprintf("Related artifacts mention different amounts: %s vs %s.", amount.TextValue, targetAmount),
				Confidence:       0.68,
				Status:           StatusProposed,
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate amount conflicts: %w", err)
		}
		rows.Close()
	}
	return dedupeRelations(out), nil
}

func relationForExactFact(factType string) (string, bool) {
	switch factType {
	case FactDueDate, FactRequestedAction:
		return RelationSameObligationAs, true
	case FactPolicyNumber, FactAccountNumber, FactVendor, FactOrganization, FactPerson, FactAppointment:
		return RelationSupports, true
	case FactDocumentTitle:
		return RelationRelatedTo, true
	default:
		return "", false
	}
}

func factsByType(facts []Fact, factType string) []Fact {
	var out []Fact
	for _, fact := range facts {
		if fact.Type == factType {
			out = append(out, fact)
		}
	}
	return out
}

func relationKeyFacts(facts []Fact) map[string]map[string]bool {
	keys := map[string]map[string]bool{}
	for _, fact := range facts {
		switch fact.Type {
		case FactVendor, FactOrganization, FactPolicyNumber, FactAccountNumber, FactDocumentTitle:
			if keys[fact.Type] == nil {
				keys[fact.Type] = map[string]bool{}
			}
			keys[fact.Type][fact.TextValue] = true
		}
	}
	return keys
}

func sharesFactKey(ctx context.Context, db *sql.DB, artifactID string, keys map[string]map[string]bool) bool {
	for factType, values := range keys {
		for value := range values {
			var one int
			err := db.QueryRowContext(ctx, `
SELECT 1
FROM extracted_fact
WHERE artifact_id = ?
	AND fact_type = ?
	AND text_value = ?
LIMIT 1
`, artifactID, factType, value).Scan(&one)
			if err == nil {
				return true
			}
		}
	}
	return false
}

func relationTitle(relation Relation) string {
	return strings.ReplaceAll(relation.Type, "_", " ") + ": " + relation.TargetArtifactID
}

func dedupeRelations(in []Relation) []Relation {
	seen := map[string]bool{}
	var out []Relation
	for _, relation := range in {
		key := relation.SourceArtifactID + "\x00" + relation.TargetArtifactID + "\x00" + relation.Type + "\x00" + relation.Reason
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, relation)
	}
	return out
}
