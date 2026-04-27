package server

import (
	"strings"
	"time"
)

var humaneCategoryLabels = map[string]string{
	"needs_action":      "Needs your attention",
	"bills_money":       "Bills & finance",
	"school_family":     "Family & school",
	"travel_events":     "Travel & events",
	"house_car":         "Home & vehicle",
	"documents_to_file": "To file",
	"interesting":       "Worth remembering",
	"unverified":        "Unverified",
}

var humaneCategoryOrder = []string{
	"needs_action",
	"bills_money",
	"school_family",
	"travel_events",
	"house_car",
	"documents_to_file",
	"interesting",
	"unverified",
}

func humaneCategory(category string) string {
	if label, ok := humaneCategoryLabels[category]; ok {
		return label
	}
	if category == "" {
		return "Other"
	}
	parts := strings.Split(category, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func humaneCategoryRank(category string) int {
	for i, key := range humaneCategoryOrder {
		if key == category {
			return i
		}
	}
	return len(humaneCategoryOrder) + 1
}

func humaneType(artifactType string) string {
	switch artifactType {
	case "pdf":
		return "Document"
	case "image":
		return "Photo"
	case "text":
		return "Note"
	case "email":
		return "Email"
	}
	if artifactType == "" {
		return "Item"
	}
	return strings.ToUpper(artifactType[:1]) + artifactType[1:]
}

func humaneTypePlural(artifactType string) string {
	switch artifactType {
	case "pdf":
		return "Documents"
	case "image":
		return "Photos"
	case "text":
		return "Notes"
	case "email":
		return "Emails"
	}
	return humaneType(artifactType) + "s"
}

func humaneSource(source string) string {
	switch source {
	case "watch_folder":
		return "Watch folder"
	case "ingest_upload":
		return "Manual upload"
	case "email":
		return "Email"
	}
	if source == "" {
		return "Unknown source"
	}
	return strings.ReplaceAll(strings.ToUpper(source[:1])+source[1:], "_", " ")
}

// humaneDate accepts an RFC3339-ish or YYYY-MM-DD string and returns a short
// human-readable representation: "Today", "Yesterday", weekday for the past
// week, or "Apr 21, 2026" otherwise. Empty input returns "".
func humaneDate(value string) string {
	t, ok := parseFlexibleTime(value)
	if !ok {
		return value
	}
	now := time.Now()
	return relativeDate(t, now)
}

// humaneRelativeDue is like humaneDate but oriented toward a future due date:
// "Today", "Tomorrow", "Friday" (this week), "Apr 28" (later), "Past due" (past).
func humaneRelativeDue(value string) string {
	t, ok := parseFlexibleTime(value)
	if !ok {
		return value
	}
	now := time.Now()
	today := startOfDay(now)
	due := startOfDay(t)
	days := int(due.Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return "Past due"
	case days == 0:
		return "Due today"
	case days == 1:
		return "Due tomorrow"
	case days < 7:
		return "Due " + due.Weekday().String()
	case days < 14:
		return "Due " + due.Format("Mon Jan 2")
	default:
		return "Due " + due.Format("Jan 2")
	}
}

// dueChipClass picks a brand chip class from a due-date string.
func dueChipClass(value string) string {
	t, ok := parseFlexibleTime(value)
	if !ok {
		return ""
	}
	today := startOfDay(time.Now())
	due := startOfDay(t)
	days := int(due.Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return "zora-chip--soon"
	case days <= 3:
		return "zora-chip--soon"
	case days <= 7:
		return "zora-chip--week"
	default:
		return "zora-chip--later"
	}
}

// parseFlexibleTime tries a handful of representations the LLM/data layer is
// likely to produce. Returns parsed time and whether it succeeded.
func parseFlexibleTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"01/02/2006",
		"Jan 2, 2006",
		"January 2, 2006",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func startOfDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func relativeDate(t, now time.Time) string {
	today := startOfDay(now)
	day := startOfDay(t)
	diffDays := int(today.Sub(day).Hours() / 24)
	switch {
	case diffDays == 0:
		return "Today"
	case diffDays == 1:
		return "Yesterday"
	case diffDays > 1 && diffDays < 7:
		return day.Weekday().String()
	case diffDays < 0 && diffDays > -7:
		return "This " + day.Weekday().String()
	default:
		if t.Year() == now.Year() {
			return day.Format("Jan 2")
		}
		return day.Format("Jan 2, 2006")
	}
}
