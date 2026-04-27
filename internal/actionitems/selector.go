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
