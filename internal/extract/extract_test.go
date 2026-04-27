package extract

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalExtractorReadsUTF8Text(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("Payment receipt\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := (LocalExtractor{}).Extract(context.Background(), path, TypeText)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if result.Text != "Payment receipt\n" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Extractor != "utf8" {
		t.Fatalf("extractor = %q", result.Extractor)
	}
}

func TestLocalExtractorRejectsInvalidUTF8Text(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.txt")
	if err := os.WriteFile(path, []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := (LocalExtractor{}).Extract(context.Background(), path, TypeText)
	if err == nil {
		t.Fatal("expected invalid UTF-8 error")
	}
	if !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("error = %v", err)
	}
}
