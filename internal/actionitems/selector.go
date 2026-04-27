package actionitems

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Selector struct {
	DB             *sql.DB
	Limit          int
	EvidenceBudget int
}

type rawCandidate struct {
	Candidate
	text string
}

var (
	moneyWords = []string{
		"invoice", "bill", "payment", "paid", "receipt", "refund", "statement",
		"balance", "subscription", "renewal", "premium", "charge", "due amount",
	}
	actionWords = []string{
		"action required", "please sign", "return by", "due", "deadline",
		"expires", "appointment", "confirm", "complete", "submit", "required",
		"respond", "renew", "schedule", "pay by",
	}
	householdWords = []string{
		"school", "teacher", "parent", "doctor", "dentist", "insurance",
		"vehicle", "registration", "tax", "mortgage", "rent", "utility",
		"passport", "permit", "form",
	}
	newsletterWords = []string{
		"unsubscribe", "sale", "promotion", "webinar", "digest", "newsletter",
		"view in browser", "manage preferences", "limited time offer",
	}
	dateLikePattern       = regexp.MustCompile(`(?i)\b(\d{1,2}/\d{1,2}(/\d{2,4})?|\d{4}-\d{2}-\d{2}|jan(uary)?|feb(ruary)?|mar(ch)?|apr(il)?|may|jun(e)?|jul(y)?|aug(ust)?|sep(tember)?|oct(ober)?|nov(ember)?|dec(ember)?|monday|tuesday|wednesday|thursday|friday|saturday|sunday|tomorrow|today|next week)\b`)
	genericImageNameRegex = regexp.MustCompile(`(?i)^(img|dsc|image)[_-]?\d+$`)
)

func (s Selector) Select(ctx context.Context, start, end time.Time) ([]Candidate, error) {
	if s.DB == nil {
		return nil, fmt.Errorf("action item selector database is required")
	}
	if !end.After(start) {
		return nil, fmt.Errorf("period end must be after period start")
	}

	rows, err := s.DB.QueryContext(ctx, `
SELECT a.id, a.type, COALESCE(a.title, ''), COALESCE(a.event_at, ''),
	a.created_at, COALESCE(e.text, '')
FROM artifact a
JOIN extracted_text e ON e.artifact_id = a.id
WHERE a.deleted_at IS NULL
	AND a.event_at IS NOT NULL
	AND a.event_at >= ?
	AND a.event_at < ?
ORDER BY a.event_at DESC, a.created_at DESC
`, formatTime(start), formatTime(end))
	if err != nil {
		return nil, fmt.Errorf("query action item candidates: %w", err)
	}
	defer rows.Close()

	var raws []rawCandidate
	for rows.Next() {
		var raw rawCandidate
		if err := rows.Scan(
			&raw.ArtifactID,
			&raw.Type,
			&raw.Title,
			&raw.EventAt,
			&raw.CreatedAt,
			&raw.text,
		); err != nil {
			return nil, fmt.Errorf("scan action item candidate: %w", err)
		}
		chunkText, err := artifactChunkText(ctx, s.DB, raw.ArtifactID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(chunkText) != "" {
			raw.text = chunkText
		}
		raw.Score, raw.Signals = Score(raw.Title, raw.Type, raw.text)
		if err := enrichCandidate(ctx, s.DB, &raw.Candidate); err != nil {
			return nil, err
		}
		boost, signals := StructuredScore(raw.Candidate)
		raw.Score += boost
		raw.Signals = append(raw.Signals, signals...)
		if raw.Score >= 0 {
			raws = append(raws, raw)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate action item candidates: %w", err)
	}

	sort.SliceStable(raws, func(i, j int) bool {
		if raws[i].Score == raws[j].Score {
			return raws[i].EventAt > raws[j].EventAt
		}
		return raws[i].Score > raws[j].Score
	})

	limit := s.Limit
	if limit < 1 {
		limit = DefaultLimit
	}
	if len(raws) > limit {
		raws = raws[:limit]
	}

	candidates := make([]Candidate, 0, len(raws))
	for _, raw := range raws {
		raw.Evidence = raw.text
		raw.text = ""
		candidates = append(candidates, raw.Candidate)
	}
	return ApplyEvidenceBudget(candidates, s.EvidenceBudget), nil
}

func Score(title, artifactType, text string) (int, []string) {
	haystack := strings.ToLower(title + "\n" + text)
	score := 0
	var signals []string

	if len(strings.TrimSpace(text)) >= 80 {
		score += 3
		signals = append(signals, "substantial_text")
	}
	if containsAny(haystack, moneyWords) {
		score += 4
		signals = append(signals, "money")
	}
	if containsAny(haystack, actionWords) {
		score += 5
		signals = append(signals, "action")
	}
	if containsAny(haystack, householdWords) {
		score += 3
		signals = append(signals, "household")
	}
	if dateLikePattern.MatchString(haystack) {
		score += 3
		signals = append(signals, "date")
	}
	if artifactType == "pdf" {
		score += 2
		signals = append(signals, "pdf")
	}
	if artifactType == "image" && len(strings.TrimSpace(text)) >= 80 {
		score += 2
		signals = append(signals, "image_ocr")
	}
	if len(strings.TrimSpace(text)) < 80 && !dateLikePattern.MatchString(haystack) {
		score -= 4
		signals = append(signals, "short_text")
	}
	if artifactType == "image" && genericImageNameRegex.MatchString(strings.TrimSpace(title)) && !containsAny(haystack, actionWords) {
		score -= 4
		signals = append(signals, "generic_image_name")
	}
	if containsAny(haystack, newsletterWords) {
		score -= 6
		signals = append(signals, "newsletter")
	}
	return score, signals
}

func StructuredScore(candidate Candidate) (int, []string) {
	score := 0
	var signals []string
	switch candidate.Class {
	case "newsletter_promo":
		score -= 6
		signals = append(signals, "class_newsletter")
	case "bill_statement", "school_family", "medical_health", "insurance_vehicle", "tax_finance", "travel_event":
		score += 4
		signals = append(signals, "class_actionable")
	case "receipt_purchase", "identity_legal":
		score += 2
		signals = append(signals, "class_useful")
	}
	for _, fact := range candidate.Facts {
		switch fact.Type {
		case "due_date", "requested_action", "appointment":
			score += 5
			signals = append(signals, "fact_action")
		case "amount_due":
			score += 5
			signals = append(signals, "fact_payment_due")
		case "payment_status":
			switch fact.TextValue {
			case "payment_due":
				score += 6
				signals = append(signals, "payment_due")
			case "paid":
				score -= 6
				signals = append(signals, "payment_paid")
			}
		case "is_payment_due":
			switch fact.TextValue {
			case "true":
				score += 6
				signals = append(signals, "payment_due")
			case "false":
				score -= 6
				signals = append(signals, "payment_not_due")
			}
		case "document_type":
			if fact.TextValue == "receipt" {
				score -= 3
				signals = append(signals, "document_receipt")
			}
		case "amount_paid":
			score -= 2
			signals = append(signals, "amount_paid")
		case "policy_number", "account_number":
			score += 3
			signals = append(signals, "fact_structured")
		case "document_title":
			score += 1
		}
	}
	if len(candidate.Relations) > 0 {
		score += 2
		signals = append(signals, "related_artifacts")
	}
	if len(candidate.BriefingHistory) > 0 {
		score -= 1
		signals = append(signals, "previously_briefed")
	}
	return score, signals
}

func ApplyEvidenceBudget(candidates []Candidate, budget int) []Candidate {
	if budget < 1 {
		budget = DefaultCharBudget
	}
	out := make([]Candidate, 0, len(candidates))
	remaining := budget
	for _, candidate := range candidates {
		if remaining < 200 {
			break
		}
		evidence := strings.TrimSpace(candidate.Evidence)
		if evidence == "" {
			continue
		}
		limit := CandidateCharLimit
		if remaining < limit {
			limit = remaining
		}
		candidate.Evidence = truncateRunes(evidence, limit)
		remaining -= len([]rune(candidate.Evidence))
		out = append(out, candidate)
	}
	return out
}

func enrichCandidate(ctx context.Context, db *sql.DB, candidate *Candidate) error {
	class, err := candidateClass(ctx, db, candidate.ArtifactID)
	if err != nil {
		return err
	}
	candidate.Class = class
	facts, err := candidateFacts(ctx, db, candidate.ArtifactID)
	if err != nil {
		return err
	}
	candidate.Facts = facts
	relations, err := candidateRelations(ctx, db, candidate.ArtifactID)
	if err != nil {
		return err
	}
	candidate.Relations = relations
	history, err := candidateBriefingHistory(ctx, db, candidate.ArtifactID)
	if err != nil {
		return err
	}
	candidate.BriefingHistory = history
	threads, err := candidateThreads(ctx, db, candidate.ArtifactID)
	if err != nil {
		return err
	}
	candidate.Threads = threads
	return nil
}

func candidateThreads(ctx context.Context, db *sql.DB, artifactID string) ([]CandidateThread, error) {
	rows, err := db.QueryContext(ctx, `
SELECT t.id, t.title, t.kind
FROM thread t
JOIN thread_member m ON m.thread_id = t.id
WHERE m.artifact_id = ?
	AND t.status = 'active'
ORDER BY t.updated_at DESC
LIMIT 4
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query candidate threads: %w", err)
	}
	defer rows.Close()
	var out []CandidateThread
	for rows.Next() {
		var t CandidateThread
		if err := rows.Scan(&t.ID, &t.Title, &t.Kind); err != nil {
			return nil, fmt.Errorf("scan candidate thread: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func candidateClass(ctx context.Context, db *sql.DB, artifactID string) (string, error) {
	var class string
	err := db.QueryRowContext(ctx, `
SELECT class
FROM artifact_classification
WHERE artifact_id = ?
`, artifactID).Scan(&class)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query candidate class: %w", err)
	}
	return class, nil
}

func candidateFacts(ctx context.Context, db *sql.DB, artifactID string) ([]CandidateFact, error) {
	rows, err := db.QueryContext(ctx, `
SELECT fact_type, text_value, COALESCE(quote, ''), confidence
FROM extracted_fact
WHERE artifact_id = ?
ORDER BY
	CASE fact_type
		WHEN 'is_payment_due' THEN 0
		WHEN 'payment_status' THEN 1
		WHEN 'amount_due' THEN 2
		WHEN 'due_date' THEN 3
		WHEN 'requested_action' THEN 4
		WHEN 'appointment' THEN 5
		WHEN 'amount_paid' THEN 6
		WHEN 'document_type' THEN 7
		WHEN 'amount' THEN 8
		ELSE 9
	END,
	confidence DESC
LIMIT 16
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query candidate facts: %w", err)
	}
	defer rows.Close()
	var facts []CandidateFact
	for rows.Next() {
		var fact CandidateFact
		if err := rows.Scan(&fact.Type, &fact.TextValue, &fact.Quote, &fact.Confidence); err != nil {
			return nil, fmt.Errorf("scan candidate fact: %w", err)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidate facts: %w", err)
	}
	return facts, nil
}

func candidateRelations(ctx context.Context, db *sql.DB, artifactID string) ([]CandidateRelation, error) {
	rows, err := db.QueryContext(ctx, `
SELECT r.relation_type,
	CASE WHEN r.source_artifact_id = ? THEN r.target_artifact_id ELSE r.source_artifact_id END AS other_artifact,
	r.reason,
	r.confidence
FROM artifact_relation r
WHERE (r.source_artifact_id = ? OR r.target_artifact_id = ?)
	AND r.status = 'proposed'
ORDER BY r.updated_at DESC
LIMIT 8
`, artifactID, artifactID, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query candidate relations: %w", err)
	}
	defer rows.Close()
	var relations []CandidateRelation
	for rows.Next() {
		var relation CandidateRelation
		if err := rows.Scan(&relation.Type, &relation.OtherArtifact, &relation.Reason, &relation.Confidence); err != nil {
			return nil, fmt.Errorf("scan candidate relation: %w", err)
		}
		relations = append(relations, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidate relations: %w", err)
	}
	return relations, nil
}

func candidateBriefingHistory(ctx context.Context, db *sql.DB, artifactID string) ([]CandidateBriefing, error) {
	rows, err := db.QueryContext(ctx, `
SELECT b.title, i.title, b.created_at
FROM briefing_item_artifact bia
JOIN briefing_item i ON i.id = bia.briefing_item_id
JOIN briefing b ON b.id = i.briefing_id
WHERE bia.artifact_id = ?
ORDER BY b.created_at DESC
LIMIT 5
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query candidate briefing history: %w", err)
	}
	defer rows.Close()
	var history []CandidateBriefing
	for rows.Next() {
		var item CandidateBriefing
		if err := rows.Scan(&item.Title, &item.ItemTitle, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan candidate briefing history: %w", err)
		}
		history = append(history, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidate briefing history: %w", err)
	}
	return history, nil
}

func artifactChunkText(ctx context.Context, db *sql.DB, artifactID string) (string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT text
FROM artifact_chunk
WHERE artifact_id = ?
ORDER BY ordinal
LIMIT 12
`, artifactID)
	if err != nil {
		return "", fmt.Errorf("query artifact chunks for action item candidate: %w", err)
	}
	defer rows.Close()

	var parts []string
	total := 0
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return "", fmt.Errorf("scan artifact chunk for action item candidate: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, text)
		total += len([]rune(text))
		if total >= CandidateCharLimit*3 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate artifact chunks for action item candidate: %w", err)
	}
	return strings.Join(parts, "\n\n"), nil
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 16 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-16])) + "\n[truncated]"
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
