package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// classificationView is the small humane summary of how the pipeline classified
// an artifact, surfaced on the artifact page outside the technical details.
type classificationView struct {
	Class       string
	Label       string
	Confidence  float64
	NeedsReview bool
	Source      string
}

type factChip struct {
	Type     string
	Label    string
	Value    string
	DueLabel string
	DueClass string
}

type relatedArtifactView struct {
	ArtifactID string
	Title      string
	Type       string
	EventAt    string
	Relation   string
	Reason     string
	Confidence float64
}

type threadLink struct {
	ID        string
	Title     string
	KindLabel string
	DateRange string
}

// loadFactSublines returns a one-line humane subline per artifact id, drawn
// from extracted facts. Empty entries are omitted from the returned map.
func loadFactSublines(ctx context.Context, db *sql.DB, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	placeholders := strings.TrimPrefix(strings.Repeat(",?", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	query := fmt.Sprintf(`
SELECT artifact_id, fact_type, text_value, confidence
FROM extracted_fact
WHERE artifact_id IN (%s)
	AND fact_type IN ('vendor', 'organization', 'amount', 'amount_due', 'due_date', 'appointment')
ORDER BY artifact_id, confidence DESC
`, placeholders)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load fact sublines: %w", err)
	}
	defer rows.Close()

	type slotted struct {
		entity string
		amount string
		due    string
	}
	bucket := map[string]*slotted{}
	for rows.Next() {
		var artifactID, factType, textValue string
		var confidence float64
		if err := rows.Scan(&artifactID, &factType, &textValue, &confidence); err != nil {
			return nil, fmt.Errorf("scan fact subline row: %w", err)
		}
		s := bucket[artifactID]
		if s == nil {
			s = &slotted{}
			bucket[artifactID] = s
		}
		switch factType {
		case "vendor", "organization":
			if s.entity == "" {
				s.entity = textValue
			}
		case "amount", "amount_due":
			if s.amount == "" {
				s.amount = textValue
			}
		case "due_date":
			if s.due == "" {
				s.due = humaneRelativeDue(textValue)
			}
		case "appointment":
			if s.due == "" {
				s.due = humaneRelativeDue(textValue)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fact subline rows: %w", err)
	}

	out := map[string]string{}
	for id, s := range bucket {
		parts := []string{}
		if s.entity != "" {
			parts = append(parts, s.entity)
		}
		if s.amount != "" {
			parts = append(parts, s.amount)
		}
		if s.due != "" {
			parts = append(parts, s.due)
		}
		if len(parts) > 0 {
			out[id] = strings.Join(parts, " · ")
		}
	}
	return out, nil
}

func loadThreadLinks(ctx context.Context, db *sql.DB, artifactID string) ([]threadLink, error) {
	rows, err := db.QueryContext(ctx, `
SELECT t.id, t.kind, t.title, COALESCE(t.date_start, ''), COALESCE(t.date_end, '')
FROM thread t
JOIN thread_member m ON m.thread_id = t.id
WHERE m.artifact_id = ?
	AND t.status <> 'dismissed'
ORDER BY t.updated_at DESC
LIMIT 8
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("load thread links: %w", err)
	}
	defer rows.Close()
	var out []threadLink
	for rows.Next() {
		var id, kind, title, dateStart, dateEnd string
		if err := rows.Scan(&id, &kind, &title, &dateStart, &dateEnd); err != nil {
			return nil, fmt.Errorf("scan thread link: %w", err)
		}
		out = append(out, threadLink{
			ID:        id,
			Title:     title,
			KindLabel: humaneThreadKind(kind),
			DateRange: humaneDateRange(dateStart, dateEnd),
		})
	}
	return out, rows.Err()
}

func loadClassificationView(ctx context.Context, db *sql.DB, artifactID string) (classificationView, bool, error) {
	row := db.QueryRowContext(ctx, `
SELECT class, confidence, source_type
FROM artifact_classification
WHERE artifact_id = ?
`, artifactID)
	var view classificationView
	if err := row.Scan(&view.Class, &view.Confidence, &view.Source); err != nil {
		if err == sql.ErrNoRows {
			return classificationView{}, false, nil
		}
		return classificationView{}, false, fmt.Errorf("load classification: %w", err)
	}
	view.Label = humaneClassLabel(view.Class)
	view.NeedsReview = view.Confidence < 0.6
	return view, true, nil
}

func loadKeyFacts(ctx context.Context, db *sql.DB, artifactID string) ([]factChip, error) {
	priority := map[string]int{
		"vendor":           1,
		"organization":     2,
		"person":           3,
		"document_title":   4,
		"document_type":    5,
		"amount":           6,
		"amount_due":       6,
		"amount_paid":      7,
		"due_date":         8,
		"appointment":      9,
		"payment_status":   10,
		"requested_action": 11,
		"policy_number":    12,
		"account_number":   13,
		"date":             14,
	}
	rows, err := db.QueryContext(ctx, `
SELECT fact_type, text_value, confidence
FROM extracted_fact
WHERE artifact_id = ?
ORDER BY confidence DESC, updated_at DESC
LIMIT 30
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("load key facts: %w", err)
	}
	defer rows.Close()
	type ranked struct {
		chip factChip
		rank int
	}
	seen := map[string]bool{}
	var collected []ranked
	for rows.Next() {
		var factType, textValue string
		var confidence float64
		if err := rows.Scan(&factType, &textValue, &confidence); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		key := factType + "|" + strings.ToLower(strings.TrimSpace(textValue))
		if seen[key] || textValue == "" {
			continue
		}
		seen[key] = true
		chip := factChip{
			Type:  factType,
			Label: humaneFactType(factType),
			Value: textValue,
		}
		if factType == "due_date" || factType == "appointment" {
			chip.DueLabel = humaneRelativeDue(textValue)
			chip.DueClass = dueChipClass(textValue)
		}
		rank, ok := priority[factType]
		if !ok {
			rank = 99
		}
		collected = append(collected, ranked{chip: chip, rank: rank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate facts: %w", err)
	}
	// stable sort by rank, preserve insertion order within rank
	for i := 1; i < len(collected); i++ {
		for j := i; j > 0 && collected[j-1].rank > collected[j].rank; j-- {
			collected[j-1], collected[j] = collected[j], collected[j-1]
		}
	}
	out := make([]factChip, 0, len(collected))
	for _, r := range collected {
		out = append(out, r.chip)
		if len(out) >= 8 {
			break
		}
	}
	return out, nil
}

func loadRelations(ctx context.Context, db *sql.DB, artifactID string) ([]relatedArtifactView, error) {
	rows, err := db.QueryContext(ctx, `
SELECT
	CASE WHEN r.source_artifact_id = ? THEN r.target_artifact_id ELSE r.source_artifact_id END AS other_id,
	r.relation_type, r.reason, r.confidence,
	COALESCE(a.title, ''), COALESCE(a.type, ''), COALESCE(a.event_at, '')
FROM artifact_relation r
JOIN artifact a ON a.id = (CASE WHEN r.source_artifact_id = ? THEN r.target_artifact_id ELSE r.source_artifact_id END)
WHERE (r.source_artifact_id = ? OR r.target_artifact_id = ?)
	AND r.status IN ('proposed', 'accepted')
	AND a.deleted_at IS NULL
ORDER BY r.confidence DESC, r.updated_at DESC
LIMIT 12
`, artifactID, artifactID, artifactID, artifactID)
	if err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}
	defer rows.Close()
	var out []relatedArtifactView
	seen := map[string]bool{}
	for rows.Next() {
		var view relatedArtifactView
		if err := rows.Scan(&view.ArtifactID, &view.Relation, &view.Reason, &view.Confidence, &view.Title, &view.Type, &view.EventAt); err != nil {
			return nil, fmt.Errorf("scan relation: %w", err)
		}
		if seen[view.ArtifactID] {
			continue
		}
		seen[view.ArtifactID] = true
		out = append(out, view)
	}
	return out, rows.Err()
}

func humaneClassLabel(class string) string {
	switch class {
	case "bill_statement":
		return "Bill"
	case "receipt_purchase":
		return "Receipt"
	case "school_family":
		return "School & family"
	case "medical_health":
		return "Medical"
	case "insurance_vehicle":
		return "Vehicle & insurance"
	case "tax_finance":
		return "Tax & finance"
	case "travel_event":
		return "Travel & event"
	case "identity_legal":
		return "Identity & legal"
	case "correspondence":
		return "Correspondence"
	case "newsletter_promo":
		return "Newsletter"
	case "photo_memory":
		return "Photo"
	case "generic_document":
		return "Document"
	}
	if class == "" {
		return ""
	}
	return strings.ReplaceAll(strings.ToUpper(class[:1])+class[1:], "_", " ")
}
