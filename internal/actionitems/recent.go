package actionitems

import (
	"context"
	"fmt"
	"time"
)

// RecentItem is a briefing item paired with the briefing it belongs to and
// the artifacts it cites. It powers the Today view, which aggregates items
// across multiple briefings.
type RecentItem struct {
	Item        RunItem
	BriefingID  string
	PeriodStart string
	PeriodEnd   string
}

// RecentItems returns briefing items from briefings created since the given
// time, ordered by briefing recency then sort order. Limit is the max number
// of items returned; pass 0 for the default of 50.
func (r Repository) RecentItems(ctx context.Context, since time.Time, limit int) ([]RecentItem, error) {
	if r.DB == nil {
		return nil, fmt.Errorf("action item repository database is required")
	}
	if limit < 1 {
		limit = 50
	}
	rows, err := r.DB.QueryContext(ctx, `
SELECT i.id, i.briefing_id, b.period_start, b.period_end,
	i.category, i.title, i.summary,
	COALESCE(i.why_it_matters, ''), COALESCE(i.action_text, ''),
	COALESCE(i.due_at, ''), i.confidence, i.source_status,
	i.sort_order, i.created_at
FROM briefing_item i
JOIN briefing b ON b.id = i.briefing_id
WHERE b.created_at >= ?
ORDER BY b.created_at DESC, i.sort_order, i.created_at
LIMIT ?
`, formatTime(since), limit)
	if err != nil {
		return nil, fmt.Errorf("query recent briefing items: %w", err)
	}
	defer rows.Close()

	var out []RecentItem
	for rows.Next() {
		var rec RecentItem
		if err := rows.Scan(
			&rec.Item.ID,
			&rec.BriefingID,
			&rec.PeriodStart,
			&rec.PeriodEnd,
			&rec.Item.Category,
			&rec.Item.Title,
			&rec.Item.Summary,
			&rec.Item.WhyItMatters,
			&rec.Item.ActionText,
			&rec.Item.DueAt,
			&rec.Item.Confidence,
			&rec.Item.SourceStatus,
			&rec.Item.SortOrder,
			&rec.Item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recent briefing item: %w", err)
		}
		artifacts, err := r.itemArtifacts(ctx, rec.Item.ID)
		if err != nil {
			return nil, err
		}
		rec.Item.Artifacts = artifacts
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent briefing items: %w", err)
	}
	return out, nil
}
