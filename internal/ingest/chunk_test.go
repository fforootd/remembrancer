package ingest

import (
	"strings"
	"testing"
)

func TestChunkMarkdownSplitsByHeadingAndPreservesPath(t *testing.T) {
	markdown := "# Root\n\nIntro\n\n## Child\n\nReceipt total due Friday."

	chunks := ChunkMarkdown("art_1", "Receipt", markdown)
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d: %+v", len(chunks), chunks)
	}
	if chunks[0].HeadingPath != "Root" {
		t.Fatalf("first heading path = %q", chunks[0].HeadingPath)
	}
	if chunks[1].HeadingPath != "Root > Child" {
		t.Fatalf("second heading path = %q", chunks[1].HeadingPath)
	}
	if chunks[0].Ordinal != 0 || chunks[1].Ordinal != 1 {
		t.Fatalf("ordinals = %d, %d", chunks[0].Ordinal, chunks[1].Ordinal)
	}
	if chunks[1].CharStart <= chunks[0].CharStart {
		t.Fatalf("expected increasing offsets: %+v", chunks)
	}
}

func TestChunkMarkdownLongSectionOverlaps(t *testing.T) {
	paragraph := strings.Repeat("alpha beta gamma ", 80)
	markdown := "# Long\n\n" + strings.Repeat(paragraph+"\n\n", 20)

	chunks := ChunkMarkdown("art_2", "Long", markdown)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %+v", chunks)
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].CharStart >= chunks[i-1].CharEnd {
			t.Fatalf("expected overlap between chunks %d and %d: %+v %+v", i-1, i, chunks[i-1], chunks[i])
		}
		if chunks[i].ID == chunks[i-1].ID {
			t.Fatalf("duplicate chunk IDs: %+v", chunks)
		}
	}
}

func TestChunkMarkdownEmptyDocument(t *testing.T) {
	if chunks := ChunkMarkdown("art_empty", "Empty", " \n\n "); len(chunks) != 0 {
		t.Fatalf("chunks = %+v", chunks)
	}
}
