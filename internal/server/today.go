package server

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	"zora/internal/actionitems"
	"zora/internal/threads"
)

// todayData drives the home/Today view.
type todayData struct {
	AppName         string
	Nav             navState
	UserName        string
	HasBriefing     bool
	LatestBriefing  actionitems.RunSummary
	ComingUp        []todayUpcoming
	Categories      []todayCategory
	WorthRemember   []todayItem
	ActiveThreads   []activeThreadView
	PeriodStartDate string
	PeriodEndDate   string
	LLMEnabled      bool
}

type todayItem struct {
	Item       actionitems.RunItem
	BriefingID string
	DueLabel   string
	DueClass   string
}

type todayCategory struct {
	Key   string
	Label string
	Items []todayItem
}

// todayUpcoming is the unified shape for "Coming up": both briefing items and
// fact-driven items (a due_date or appointment surfaced directly from the
// understanding pipeline) flow through this struct so the template renders one
// list.
type todayUpcoming struct {
	Title    string
	Subtitle string
	DueLabel string
	DueClass string
	Link     string
	// Source is "briefing" or "artifact" — only used for tagging.
	Source string
}

type activeThreadView struct {
	Thread    threads.Thread
	KindLabel string
	DateRange string
}

func (s *Server) renderToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	since := now.AddDate(0, 0, -30).UTC()

	repo := actionitems.Repository{DB: s.database}
	recent, err := repo.RecentItems(ctx, since, 200)
	if err != nil {
		s.logger.Error("read recent briefing items", "error", err)
		http.Error(w, "read recent briefing items", http.StatusInternalServerError)
		return
	}
	runs, err := repo.RecentRuns(ctx, 1)
	if err != nil {
		s.logger.Error("read latest briefing", "error", err)
		http.Error(w, "read latest briefing", http.StatusInternalServerError)
		return
	}

	upcomingFacts, err := upcomingFromFacts(ctx, s.database, now, 14)
	if err != nil {
		s.logger.Error("read upcoming facts", "error", err)
		http.Error(w, "read upcoming facts", http.StatusInternalServerError)
		return
	}

	threadRepo := threads.Repository{DB: s.database}
	activeThreads, err := threadRepo.RecentThreads(ctx, 5)
	if err != nil {
		s.logger.Error("read recent threads", "error", err)
		http.Error(w, "read recent threads", http.StatusInternalServerError)
		return
	}

	start, end := defaultActionItemPeriod(now.UTC())
	data := todayData{
		AppName:         "Zora",
		Nav:             navFor(r.URL.Path),
		UserName:        s.cfg.User.DisplayName,
		LLMEnabled:      s.cfg.LLM.Enabled,
		PeriodStartDate: start.Format("2006-01-02"),
		PeriodEndDate:   end.Format("2006-01-02"),
		ActiveThreads:   buildActiveThreadViews(activeThreads),
	}

	if len(runs) > 0 {
		data.LatestBriefing = runs[0]
		data.HasBriefing = true
	}

	data.ComingUp = mergeUpcoming(comingUpFromBriefings(recent, now, 14), upcomingFacts)

	if data.HasBriefing {
		data.Categories = groupLatestByCategory(recent, data.LatestBriefing.ID)
	}
	data.WorthRemember = worthRememberingItems(recent)

	s.render(w, "today.html", data)
}

func buildActiveThreadViews(list []threads.Thread) []activeThreadView {
	out := make([]activeThreadView, 0, len(list))
	for _, t := range list {
		out = append(out, activeThreadView{
			Thread:    t,
			KindLabel: humaneThreadKind(t.Kind),
			DateRange: humaneDateRange(t.DateStart, t.DateEnd),
		})
	}
	return out
}

func comingUpFromBriefings(recent []actionitems.RecentItem, now time.Time, withinDays int) []todayUpcoming {
	type dated struct {
		entry todayUpcoming
		due   time.Time
	}
	var bucket []dated
	cutoff := startOfDay(now).AddDate(0, 0, withinDays)
	yesterday := startOfDay(now).AddDate(0, 0, -1)

	for _, rec := range recent {
		due, ok := parseFlexibleTime(rec.Item.DueAt)
		if !ok {
			continue
		}
		dueDay := startOfDay(due)
		if dueDay.Before(yesterday) || dueDay.After(cutoff) {
			continue
		}
		title := rec.Item.Title
		subtitle := humaneCategory(rec.Item.Category)
		link := "/briefings/" + rec.BriefingID
		bucket = append(bucket, dated{
			entry: todayUpcoming{
				Title:    title,
				Subtitle: subtitle,
				DueLabel: humaneRelativeDue(rec.Item.DueAt),
				DueClass: dueChipClass(rec.Item.DueAt),
				Link:     link,
				Source:   "briefing",
			},
			due: dueDay,
		})
	}

	sort.SliceStable(bucket, func(i, j int) bool {
		return bucket[i].due.Before(bucket[j].due)
	})
	out := make([]todayUpcoming, len(bucket))
	for i, d := range bucket {
		out[i] = d.entry
	}
	return out
}

func upcomingFromFacts(ctx context.Context, database *sql.DB, now time.Time, withinDays int) ([]todayUpcoming, error) {
	if database == nil {
		return nil, nil
	}
	cutoff := startOfDay(now).AddDate(0, 0, withinDays)
	floor := startOfDay(now).AddDate(0, 0, -1)

	rows, err := database.QueryContext(ctx, `
SELECT f.artifact_id, f.fact_type, f.text_value, f.value_json,
	COALESCE(a.title, ''), COALESCE(a.type, '')
FROM extracted_fact f
JOIN artifact a ON a.id = f.artifact_id
WHERE f.fact_type IN ('due_date', 'appointment')
	AND a.deleted_at IS NULL
	AND f.created_at >= ?
ORDER BY f.created_at DESC
LIMIT 200
`, now.AddDate(0, 0, -90).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type dated struct {
		entry todayUpcoming
		due   time.Time
		key   string
	}
	var bucket []dated
	for rows.Next() {
		var artifactID, factType, textValue, _valueJSON, title, artifactType string
		if err := rows.Scan(&artifactID, &factType, &textValue, &_valueJSON, &title, &artifactType); err != nil {
			return nil, err
		}
		due, ok := parseFlexibleTime(textValue)
		if !ok {
			continue
		}
		dueDay := startOfDay(due)
		if dueDay.Before(floor) || dueDay.After(cutoff) {
			continue
		}
		entry := todayUpcoming{
			Title:    artifactTitleOrFallback(title, artifactID),
			Subtitle: factSubtitleForArtifact(ctx, database, artifactID, factType, artifactType),
			DueLabel: humaneRelativeDue(textValue),
			DueClass: dueChipClass(textValue),
			Link:     "/library/" + artifactID,
			Source:   "artifact",
		}
		bucket = append(bucket, dated{
			entry: entry,
			due:   dueDay,
			key:   artifactID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(bucket, func(i, j int) bool {
		return bucket[i].due.Before(bucket[j].due)
	})

	seen := map[string]bool{}
	out := make([]todayUpcoming, 0, len(bucket))
	for _, d := range bucket {
		if seen[d.key] {
			continue
		}
		seen[d.key] = true
		out = append(out, d.entry)
	}
	return out, nil
}

func artifactTitleOrFallback(title, artifactID string) string {
	if title != "" {
		return title
	}
	return artifactID
}

func factSubtitleForArtifact(ctx context.Context, database *sql.DB, artifactID, factType, artifactType string) string {
	row := database.QueryRowContext(ctx, `
SELECT text_value
FROM extracted_fact
WHERE artifact_id = ? AND fact_type IN ('vendor', 'organization', 'person')
ORDER BY confidence DESC, updated_at DESC
LIMIT 1
`, artifactID)
	var entity string
	if err := row.Scan(&entity); err == nil && entity != "" {
		return humaneType(artifactType) + " · " + titleCaseSubtitle(entity)
	}
	return humaneType(artifactType)
}

func titleCaseSubtitle(value string) string {
	if value == "" {
		return ""
	}
	out := []rune(value)
	out[0] = []rune(toUpperString(string(out[0])))[0]
	return string(out)
}

func toUpperString(s string) string {
	if s == "" {
		return ""
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-32) + s[1:]
	}
	return s
}

func mergeUpcoming(briefingItems, factItems []todayUpcoming) []todayUpcoming {
	merged := make([]todayUpcoming, 0, len(briefingItems)+len(factItems))
	merged = append(merged, briefingItems...)
	for _, f := range factItems {
		if upcomingHasTitle(merged, f.Title) {
			continue
		}
		merged = append(merged, f)
	}
	return merged
}

func upcomingHasTitle(in []todayUpcoming, title string) bool {
	for _, item := range in {
		if item.Title == title {
			return true
		}
	}
	return false
}

func groupLatestByCategory(recent []actionitems.RecentItem, briefingID string) []todayCategory {
	groups := map[string][]todayItem{}
	for _, rec := range recent {
		if rec.BriefingID != briefingID {
			continue
		}
		if rec.Item.Category == "interesting" {
			continue
		}
		groups[rec.Item.Category] = append(groups[rec.Item.Category], todayItem{
			Item:       rec.Item,
			BriefingID: rec.BriefingID,
			DueLabel:   humaneRelativeDue(rec.Item.DueAt),
			DueClass:   dueChipClass(rec.Item.DueAt),
		})
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return humaneCategoryRank(keys[i]) < humaneCategoryRank(keys[j])
	})

	out := make([]todayCategory, 0, len(keys))
	for _, k := range keys {
		out = append(out, todayCategory{
			Key:   k,
			Label: humaneCategory(k),
			Items: groups[k],
		})
	}
	return out
}

func worthRememberingItems(recent []actionitems.RecentItem) []todayItem {
	var out []todayItem
	for _, rec := range recent {
		if rec.Item.Category != "interesting" {
			continue
		}
		out = append(out, todayItem{
			Item:       rec.Item,
			BriefingID: rec.BriefingID,
			DueLabel:   humaneRelativeDue(rec.Item.DueAt),
			DueClass:   dueChipClass(rec.Item.DueAt),
		})
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func groupRunItemsByCategory(items []actionitems.RunItem) []todayCategory {
	groups := map[string][]todayItem{}
	for _, item := range items {
		groups[item.Category] = append(groups[item.Category], todayItem{
			Item:     item,
			DueLabel: humaneRelativeDue(item.DueAt),
			DueClass: dueChipClass(item.DueAt),
		})
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return humaneCategoryRank(keys[i]) < humaneCategoryRank(keys[j])
	})
	out := make([]todayCategory, 0, len(keys))
	for _, k := range keys {
		out = append(out, todayCategory{
			Key:   k,
			Label: humaneCategory(k),
			Items: groups[k],
		})
	}
	return out
}
