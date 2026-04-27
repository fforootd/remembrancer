package server

import (
	"net/http"
	"sort"
	"time"

	"zora/internal/actionitems"
)

// todayData drives the home/Today view.
type todayData struct {
	AppName         string
	Nav             navState
	UserName        string
	HasBriefing     bool
	LatestBriefing  actionitems.RunSummary
	ComingUp        []todayItem
	Categories      []todayCategory
	WorthRemember   []todayItem
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

	start, end := defaultActionItemPeriod(now.UTC())
	data := todayData{
		AppName:         "Zora",
		Nav:             navFor(r.URL.Path),
		UserName:        s.cfg.User.DisplayName,
		LLMEnabled:      s.cfg.LLM.Enabled,
		PeriodStartDate: start.Format("2006-01-02"),
		PeriodEndDate:   end.Format("2006-01-02"),
	}

	if len(runs) > 0 {
		data.LatestBriefing = runs[0]
		data.HasBriefing = true
	}

	data.ComingUp = comingUpItems(recent, now, 14)

	if data.HasBriefing {
		data.Categories = groupLatestByCategory(recent, data.LatestBriefing.ID)
	}
	data.WorthRemember = worthRememberingItems(recent)

	s.render(w, "today.html", data)
}

func comingUpItems(recent []actionitems.RecentItem, now time.Time, withinDays int) []todayItem {
	type dated struct {
		item todayItem
		due  time.Time
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
		bucket = append(bucket, dated{
			item: todayItem{
				Item:       rec.Item,
				BriefingID: rec.BriefingID,
				DueLabel:   humaneRelativeDue(rec.Item.DueAt),
				DueClass:   dueChipClass(rec.Item.DueAt),
			},
			due: dueDay,
		})
	}

	sort.SliceStable(bucket, func(i, j int) bool {
		return bucket[i].due.Before(bucket[j].due)
	})

	out := make([]todayItem, len(bucket))
	for i, d := range bucket {
		out[i] = d.item
	}
	return out
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

