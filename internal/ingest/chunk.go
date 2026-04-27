package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const (
	chunkTargetChars  = 3000
	chunkOverlapChars = 300
)

var headingLinePattern = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

type ArtifactChunk struct {
	ID           string
	Ordinal      int
	Title        string
	Text         string
	HeadingPath  string
	CharStart    int
	CharEnd      int
	MetadataJSON string
}

func ChunkMarkdown(artifactID, title, markdown string) []ArtifactChunk {
	if strings.TrimSpace(markdown) == "" {
		return nil
	}

	var chunks []ArtifactChunk
	for _, section := range markdownSections(markdown) {
		for _, window := range chunkSection(markdown, section) {
			text, start, end := trimWindow(markdown, window.start, window.end)
			if text == "" {
				continue
			}
			chunks = append(chunks, ArtifactChunk{
				ID:          ChunkID(artifactID, len(chunks)),
				Ordinal:     len(chunks),
				Title:       title,
				Text:        text,
				HeadingPath: section.headingPath,
				CharStart:   start,
				CharEnd:     end,
			})
		}
	}
	return chunks
}

func ChunkID(artifactID string, ordinal int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", artifactID, ordinal)))
	return "chk_" + hex.EncodeToString(sum[:16])
}

type markdownSection struct {
	start       int
	end         int
	headingPath string
}

type chunkWindow struct {
	start int
	end   int
}

func markdownSections(markdown string) []markdownSection {
	var sections []markdownSection
	headings := map[int]string{}
	currentStart := 0
	currentHeadingPath := ""

	for offset := 0; offset < len(markdown); {
		lineEnd := strings.IndexByte(markdown[offset:], '\n')
		next := len(markdown)
		line := markdown[offset:]
		if lineEnd >= 0 {
			next = offset + lineEnd + 1
			line = markdown[offset : offset+lineEnd]
		}

		if match := headingLinePattern.FindStringSubmatch(line); match != nil {
			if offset > currentStart {
				sections = append(sections, markdownSection{
					start:       currentStart,
					end:         offset,
					headingPath: currentHeadingPath,
				})
			}
			level := len(match[1])
			for existing := range headings {
				if existing >= level {
					delete(headings, existing)
				}
			}
			headings[level] = strings.TrimSpace(match[2])
			currentStart = offset
			currentHeadingPath = joinHeadingPath(headings)
		}

		offset = next
	}

	if currentStart < len(markdown) {
		sections = append(sections, markdownSection{
			start:       currentStart,
			end:         len(markdown),
			headingPath: currentHeadingPath,
		})
	}
	return sections
}

func chunkSection(markdown string, section markdownSection) []chunkWindow {
	if section.end-section.start <= chunkTargetChars {
		return []chunkWindow{{start: section.start, end: section.end}}
	}

	var windows []chunkWindow
	for start := section.start; start < section.end; {
		maxEnd := start + chunkTargetChars
		if maxEnd >= section.end {
			windows = append(windows, chunkWindow{start: start, end: section.end})
			break
		}

		end := paragraphBoundary(markdown, section, start, maxEnd)
		windows = append(windows, chunkWindow{start: start, end: end})
		nextStart := end - chunkOverlapChars
		if nextStart <= start {
			nextStart = end
		}
		start = nextStart
	}
	return windows
}

func paragraphBoundary(markdown string, section markdownSection, start, maxEnd int) int {
	minEnd := start + chunkTargetChars/2
	if minEnd >= maxEnd {
		return maxEnd
	}
	window := markdown[minEnd:maxEnd]
	if idx := strings.LastIndex(window, "\n\n"); idx >= 0 {
		return minEnd + idx + 2
	}
	if idx := strings.LastIndex(window, "\n"); idx >= 0 {
		return minEnd + idx + 1
	}
	if maxEnd > section.end {
		return section.end
	}
	return maxEnd
}

func trimWindow(markdown string, start, end int) (string, int, int) {
	for start < end {
		r := markdown[start]
		if r != ' ' && r != '\n' && r != '\t' && r != '\r' {
			break
		}
		start++
	}
	for end > start {
		r := markdown[end-1]
		if r != ' ' && r != '\n' && r != '\t' && r != '\r' {
			break
		}
		end--
	}
	return markdown[start:end], start, end
}

func joinHeadingPath(headings map[int]string) string {
	var parts []string
	for level := 1; level <= 6; level++ {
		if heading := headings[level]; heading != "" {
			parts = append(parts, heading)
		}
	}
	return strings.Join(parts, " > ")
}
