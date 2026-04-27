package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	threadAutoAddScore       = 2.0
	threadProposeScore       = 1.0
	threadProximityWideDays  = 60
	threadProximityCloseDays = 14
)

var threadSignatureFactTypes = []string{
	FactVendor,
	FactOrganization,
	FactPerson,
	FactPolicyNumber,
	FactAccountNumber,
	FactDocumentTitle,
}

var threadStrongRelations = map[string]bool{
	RelationSameObligationAs: true,
	RelationSupersedes:       true,
	RelationUpdatesFact:      true,
	RelationSupports:         true,
}

func ClusterArtifact(ctx context.Context, db *sql.DB, artifactID string, now time.Time) ([]ThreadAssignment, error) {
	snapshot, ok, err := LoadArtifactSnapshot(ctx, db, artifactID)
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

	if !threadEligible(classification.Class) {
		return nil, nil
	}

	signature := buildThreadSignature(facts)
	if len(signature) == 0 && classification.Class == "" {
		return nil, nil
	}

	eventAt := snapshot.EventAt
	if eventAt == "" {
		eventAt = snapshot.CreatedAt
	}

	candidates, err := findCandidateThreads(ctx, db, artifactID, classification.Class, signature, eventAt)
	if err != nil {
		return nil, err
	}

	relations, err := loadStrongRelationTargets(ctx, db, artifactID)
	if err != nil {
		return nil, err
	}
	relationCandidates, err := threadsForArtifacts(ctx, db, artifactID, relations)
	if err != nil {
		return nil, err
	}
	for id, c := range relationCandidates {
		if existing, ok := candidates[id]; ok {
			existing.Score += c.Score
			candidates[id] = existing
		} else {
			candidates[id] = c
		}
	}

	scored := scoreCandidates(candidates, classification.Class, eventAt)

	var best *threadCandidate
	if len(scored) > 0 {
		best = &scored[0]
	}

	if best != nil && best.Score >= threadAutoAddScore {
		if err := addArtifactToThread(ctx, db, best.ThreadID, artifactID, best.Score, ThreadMemberSourceRule, eventAt, now); err != nil {
			return nil, err
		}
		return []ThreadAssignment{{
			ThreadID:   best.ThreadID,
			ArtifactID: artifactID,
			Score:      best.Score,
			Source:     ThreadMemberSourceRule,
		}}, nil
	}

	threadID, err := createThreadForArtifact(ctx, db, artifactID, classification.Class, facts, signature, eventAt, now)
	if err != nil {
		return nil, err
	}
	return []ThreadAssignment{{
		ThreadID:   threadID,
		ArtifactID: artifactID,
		Score:      1.0,
		Source:     ThreadMemberSourceRule,
		NewThread:  true,
	}}, nil
}

type threadCandidate struct {
	ThreadID  string
	Kind      string
	DateStart string
	DateEnd   string
	Status    string
	Score     float64
	Reasons   []string
}

func threadEligible(class string) bool {
	switch class {
	case ClassNewsletterPromo, ClassPhotoMemory, "":
		return false
	}
	return true
}

func buildThreadSignature(facts []Fact) map[string]map[string]bool {
	sig := map[string]map[string]bool{}
	for _, fact := range facts {
		if !isSignatureFact(fact.Type) {
			continue
		}
		value := normalize(fact.TextValue)
		if value == "" {
			continue
		}
		if sig[fact.Type] == nil {
			sig[fact.Type] = map[string]bool{}
		}
		sig[fact.Type][value] = true
	}
	return sig
}

func isSignatureFact(factType string) bool {
	for _, t := range threadSignatureFactTypes {
		if t == factType {
			return true
		}
	}
	return false
}

func findCandidateThreads(
	ctx context.Context,
	db *sql.DB,
	artifactID, class string,
	signature map[string]map[string]bool,
	eventAt string,
) (map[string]*threadCandidate, error) {
	out := map[string]*threadCandidate{}
	if len(signature) == 0 {
		return out, nil
	}

	for factType, values := range signature {
		for value := range values {
			rows, err := db.QueryContext(ctx, `
SELECT t.id, t.kind, COALESCE(t.date_start, ''), COALESCE(t.date_end, ''), t.status
FROM thread t
JOIN thread_member m ON m.thread_id = t.id
JOIN extracted_fact f ON f.artifact_id = m.artifact_id
WHERE m.artifact_id <> ?
	AND f.fact_type = ?
	AND LOWER(f.text_value) = ?
	AND t.status = 'active'
GROUP BY t.id
LIMIT 16
`, artifactID, factType, value)
			if err != nil {
				return nil, fmt.Errorf("query candidate threads: %w", err)
			}
			for rows.Next() {
				var c threadCandidate
				if err := rows.Scan(&c.ThreadID, &c.Kind, &c.DateStart, &c.DateEnd, &c.Status); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan candidate thread: %w", err)
				}
				existing, ok := out[c.ThreadID]
				if !ok {
					existing = &c
					out[c.ThreadID] = existing
				}
				weight := 1.0
				if factType == FactPolicyNumber || factType == FactAccountNumber {
					weight = 2.0
				}
				existing.Score += weight
				existing.Reasons = append(existing.Reasons, fmt.Sprintf("shared %s", factType))
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("iterate candidate threads: %w", err)
			}
			rows.Close()
		}
	}
	_ = eventAt
	_ = class
	return out, nil
}

func loadStrongRelationTargets(ctx context.Context, db *sql.DB, artifactID string) ([]string, error) {
	relTypes := make([]string, 0, len(threadStrongRelations))
	for k := range threadStrongRelations {
		relTypes = append(relTypes, k)
	}
	if len(relTypes) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimPrefix(strings.Repeat(",?", len(relTypes)), ",")
	query := fmt.Sprintf(`
SELECT DISTINCT target_artifact_id FROM artifact_relation
WHERE source_artifact_id = ?
	AND relation_type IN (%s)
	AND status IN ('proposed', 'accepted')
UNION
SELECT DISTINCT source_artifact_id FROM artifact_relation
WHERE target_artifact_id = ?
	AND relation_type IN (%s)
	AND status IN ('proposed', 'accepted')
LIMIT 32
`, placeholders, placeholders)
	args := make([]any, 0, 2+len(relTypes)*2)
	args = append(args, artifactID)
	for _, t := range relTypes {
		args = append(args, t)
	}
	args = append(args, artifactID)
	for _, t := range relTypes {
		args = append(args, t)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query strong relations: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan strong relation: %w", err)
		}
		if id != "" && id != artifactID {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

func threadsForArtifacts(ctx context.Context, db *sql.DB, artifactID string, peers []string) (map[string]*threadCandidate, error) {
	out := map[string]*threadCandidate{}
	if len(peers) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat(",?", len(peers))
	placeholders = placeholders[1:]
	args := make([]any, 0, len(peers)+1)
	for _, p := range peers {
		args = append(args, p)
	}
	args = append(args, artifactID)
	query := fmt.Sprintf(`
SELECT t.id, t.kind, COALESCE(t.date_start, ''), COALESCE(t.date_end, ''), t.status
FROM thread t
JOIN thread_member m ON m.thread_id = t.id
WHERE m.artifact_id IN (%s)
	AND m.artifact_id <> ?
	AND t.status = 'active'
GROUP BY t.id
LIMIT 16
`, placeholders)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query threads for relation peers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c threadCandidate
		if err := rows.Scan(&c.ThreadID, &c.Kind, &c.DateStart, &c.DateEnd, &c.Status); err != nil {
			return nil, fmt.Errorf("scan related thread: %w", err)
		}
		c.Score = 1.5
		c.Reasons = []string{"strong relation"}
		out[c.ThreadID] = &c
	}
	return out, rows.Err()
}

func scoreCandidates(candidates map[string]*threadCandidate, class, eventAt string) []threadCandidate {
	now := parseAnyTime(eventAt)
	scored := make([]threadCandidate, 0, len(candidates))
	for _, c := range candidates {
		score := c.Score
		if class != "" && threadKindForClass(class, nil) == c.Kind {
			score += 0.5
		}
		score += proximityBonus(c.DateStart, c.DateEnd, now)
		c.Score = score
		scored = append(scored, *c)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored
}

func proximityBonus(dateStart, dateEnd string, ref time.Time) float64 {
	if ref.IsZero() {
		return 0
	}
	start := parseAnyTime(dateStart)
	end := parseAnyTime(dateEnd)
	if start.IsZero() && end.IsZero() {
		return 0
	}
	if start.IsZero() {
		start = end
	}
	if end.IsZero() {
		end = start
	}
	var distance time.Duration
	switch {
	case ref.Before(start):
		distance = start.Sub(ref)
	case ref.After(end):
		distance = ref.Sub(end)
	default:
		distance = 0
	}
	days := int(distance.Hours()/24) + 0
	if days <= threadProximityCloseDays {
		return 0.5
	}
	if days <= threadProximityWideDays {
		return 0.25
	}
	return -0.25
}

func parseAnyTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func threadKindForClass(class string, facts []Fact) string {
	switch class {
	case ClassBillStatement, ClassReceiptPurchase:
		return ThreadKindVendorAccount
	case ClassMedicalHealth:
		return ThreadKindVisit
	case ClassInsuranceVehicle:
		return ThreadKindVehicle
	case ClassSchoolFamily:
		return ThreadKindSchoolYear
	case ClassTravelEvent:
		return ThreadKindTravel
	case ClassTaxFinance, ClassIdentityLegal, ClassCorrespondence, ClassGenericDocument:
		return ThreadKindOther
	}
	_ = facts
	return ThreadKindOther
}

func dominantSignatureValue(signature map[string]map[string]bool, factType string) string {
	values := signature[factType]
	if len(values) == 0 {
		return ""
	}
	for v := range values {
		return v
	}
	return ""
}

func deriveThreadTitle(class string, signature map[string]map[string]bool, facts []Fact, eventAt string) string {
	classLabel := humanClassLabel(class)
	entity := dominantSignatureValue(signature, FactVendor)
	if entity == "" {
		entity = dominantSignatureValue(signature, FactOrganization)
	}
	if entity == "" {
		entity = dominantSignatureValue(signature, FactPerson)
	}
	if entity == "" {
		for _, fact := range facts {
			if fact.Type == FactDocumentTitle && strings.TrimSpace(fact.TextValue) != "" {
				entity = strings.TrimSpace(fact.TextValue)
				break
			}
		}
	}
	month := monthYearLabel(parseAnyTime(eventAt))
	parts := []string{}
	if classLabel != "" {
		parts = append(parts, classLabel)
	}
	if entity != "" {
		parts = append(parts, titleCaseEntity(entity))
	}
	if month != "" {
		parts = append(parts, month)
	}
	if len(parts) == 0 {
		return "Untitled thread"
	}
	return strings.Join(parts, " · ")
}

func humanClassLabel(class string) string {
	switch class {
	case ClassBillStatement:
		return "Bill"
	case ClassReceiptPurchase:
		return "Receipt"
	case ClassSchoolFamily:
		return "School & family"
	case ClassMedicalHealth:
		return "Medical"
	case ClassInsuranceVehicle:
		return "Vehicle"
	case ClassTaxFinance:
		return "Tax & finance"
	case ClassTravelEvent:
		return "Travel"
	case ClassIdentityLegal:
		return "Identity & legal"
	case ClassCorrespondence:
		return "Correspondence"
	case ClassGenericDocument:
		return "Document"
	}
	return ""
}

func titleCaseEntity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Fields(value)
	for i, part := range parts {
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func monthYearLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("Jan 2006")
}

func addArtifactToThread(ctx context.Context, db *sql.DB, threadID, artifactID string, score float64, source, eventAt string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin add thread member: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_member (thread_id, artifact_id, score, source, added_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(thread_id, artifact_id) DO UPDATE SET
	score = MAX(thread_member.score, excluded.score),
	source = excluded.source,
	added_at = excluded.added_at
`, threadID, artifactID, score, source, formatTime(now)); err != nil {
		return fmt.Errorf("insert thread member: %w", err)
	}

	if eventAt != "" {
		if _, err := tx.ExecContext(ctx, `
UPDATE thread
SET date_start = CASE
		WHEN date_start IS NULL OR date_start = '' THEN ?
		WHEN ? < date_start THEN ?
		ELSE date_start
	END,
	date_end = CASE
		WHEN date_end IS NULL OR date_end = '' THEN ?
		WHEN ? > date_end THEN ?
		ELSE date_end
	END,
	updated_at = ?
WHERE id = ?
`, eventAt, eventAt, eventAt, eventAt, eventAt, eventAt, formatTime(now), threadID); err != nil {
			return fmt.Errorf("update thread date range: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE thread SET updated_at = ? WHERE id = ?`, formatTime(now), threadID); err != nil {
			return fmt.Errorf("update thread timestamp: %w", err)
		}
	}

	return tx.Commit()
}

func createThreadForArtifact(
	ctx context.Context,
	db *sql.DB,
	artifactID, class string,
	facts []Fact,
	signature map[string]map[string]bool,
	eventAt string,
	now time.Time,
) (string, error) {
	threadID := hashID("thr", artifactID, class, formatTime(now))
	kind := threadKindForClass(class, facts)
	title := deriveThreadTitle(class, signature, facts, eventAt)
	signatureJSON, err := serializeSignature(signature)
	if err != nil {
		return "", err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin create thread: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO thread (
	id, kind, title, summary, date_start, date_end, status, signature_json,
	created_at, updated_at
) VALUES (?, ?, ?, NULL, NULLIF(?, ''), NULLIF(?, ''), 'active', ?, ?, ?)
`,
		threadID,
		kind,
		title,
		eventAt,
		eventAt,
		signatureJSON,
		formatTime(now),
		formatTime(now),
	); err != nil {
		return "", fmt.Errorf("insert thread: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_member (thread_id, artifact_id, score, source, added_at)
VALUES (?, ?, ?, ?, ?)
`,
		threadID,
		artifactID,
		1.0,
		ThreadMemberSourceRule,
		formatTime(now),
	); err != nil {
		return "", fmt.Errorf("insert thread member: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit thread create: %w", err)
	}
	return threadID, nil
}

func serializeSignature(signature map[string]map[string]bool) (string, error) {
	if len(signature) == 0 {
		return "{}", nil
	}
	flat := map[string][]string{}
	for factType, values := range signature {
		out := make([]string, 0, len(values))
		for v := range values {
			out = append(out, v)
		}
		sort.Strings(out)
		flat[factType] = out
	}
	data, err := json.Marshal(flat)
	if err != nil {
		return "", fmt.Errorf("marshal signature: %w", err)
	}
	return string(data), nil
}
