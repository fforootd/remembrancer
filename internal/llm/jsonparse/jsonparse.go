// Package jsonparse provides defensive parsing helpers for LLM-produced JSON.
// Local models (especially smaller, instruction-tuned ones) often wrap JSON
// in a markdown code fence — e.g. "```json\n{...}\n```" — even when the
// system prompt asks for raw JSON. UnmarshalLenient strips that wrapping
// before unmarshalling.
package jsonparse

import (
	"encoding/json"
	"fmt"
	"strings"
)

// UnmarshalLenient is encoding/json.Unmarshal with two extra steps applied
// to the input: trim surrounding whitespace, and strip a single surrounding
// markdown code fence (``` or ```json / ```javascript / etc.). If neither is
// present, behavior matches json.Unmarshal exactly.
func UnmarshalLenient(data []byte, target any) error {
	clean := stripCodeFence(string(data))
	if clean == "" {
		return fmt.Errorf("empty payload after fence strip")
	}
	if err := json.Unmarshal([]byte(clean), target); err != nil {
		return err
	}
	return nil
}

// stripCodeFence removes a single matching pair of triple-backtick fences
// from the beginning and end of the input, ignoring an optional language tag
// after the opening fence. Surrounding whitespace is also trimmed.
func stripCodeFence(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	// Drop the opening fence and any language tag on the same line.
	rest := strings.TrimPrefix(trimmed, "```")
	if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
		rest = rest[idx+1:]
	}
	rest = strings.TrimSpace(rest)
	// Drop the trailing fence.
	if cut := strings.LastIndex(rest, "```"); cut >= 0 {
		rest = strings.TrimSpace(rest[:cut])
	}
	return rest
}
