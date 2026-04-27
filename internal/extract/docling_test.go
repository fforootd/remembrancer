package extract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDoclingExtractorPostsMultipartAndParsesResult(t *testing.T) {
	var gotFormats []string
	var gotAPIKey string
	var gotFilename string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/convert/file" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		gotAPIKey = r.Header.Get("X-Api-Key")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		gotFormats = r.MultipartForm.Value["to_formats"]
		if r.MultipartForm.Value["from_formats"][0] != "pdf" {
			t.Fatalf("from_formats = %#v", r.MultipartForm.Value["from_formats"])
		}
		if r.MultipartForm.Value["do_ocr"][0] != "true" {
			t.Fatalf("do_ocr = %#v", r.MultipartForm.Value["do_ocr"])
		}
		if r.MultipartForm.Value["force_ocr"][0] != "false" {
			t.Fatalf("force_ocr = %#v", r.MultipartForm.Value["force_ocr"])
		}
		if r.MultipartForm.Value["table_mode"][0] != "accurate" {
			t.Fatalf("table_mode = %#v", r.MultipartForm.Value["table_mode"])
		}
		if files := r.MultipartForm.File["files"]; len(files) != 1 {
			t.Fatalf("files = %#v", files)
		} else {
			gotFilename = files[0].Filename
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Docling-Version", "1.17.0")
		json.NewEncoder(w).Encode(map[string]any{
			"document": map[string]any{
				"md_content":   "# Receipt\n\nTotal due",
				"text_content": "Receipt\nTotal due",
				"json_content": map[string]any{"schema": "docling"},
			},
			"status":          "success",
			"processing_time": 1.25,
			"timings":         map[string]any{"convert": 1.25},
			"errors":          []any{},
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "receipt.pdf")
	if err := os.WriteFile(path, []byte("%PDF fake"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := (DoclingExtractor{
		BaseURL:         server.URL,
		APIKey:          "secret",
		OutputFormats:   []string{"md", "text", "json"},
		DoOCR:           true,
		TableMode:       "accurate",
		ImageExportMode: "placeholder",
		Timeout:         time.Minute,
	}).Extract(context.Background(), path, TypePDF)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if gotAPIKey != "secret" {
		t.Fatalf("api key = %q", gotAPIKey)
	}
	if gotFilename != "receipt.pdf" {
		t.Fatalf("filename = %q", gotFilename)
	}
	if !reflect.DeepEqual(gotFormats, []string{"md", "text", "json"}) {
		t.Fatalf("to_formats = %#v", gotFormats)
	}
	if result.Markdown != "# Receipt\n\nTotal due" || result.Text != "Receipt\nTotal due" {
		t.Fatalf("result = %+v", result)
	}
	if result.StructuredJSON == "" || result.MetadataJSON == "" {
		t.Fatalf("expected structured and metadata JSON, got %+v", result)
	}
	if result.Extractor != "docling" || result.ExtractorVersion != "1.17.0" {
		t.Fatalf("extractor = %s version = %s", result.Extractor, result.ExtractorVersion)
	}
	if result.ProcessingTime != 1250*time.Millisecond {
		t.Fatalf("processing time = %s", result.ProcessingTime)
	}
}

func TestDoclingExtractorReturnsRetryableConversionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"document": map[string]any{},
			"status":   "failure",
			"errors":   []string{"ocr failed"},
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "scan.png")
	if err := os.WriteFile(path, []byte("fake image"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := (DoclingExtractor{BaseURL: server.URL, Timeout: time.Minute}).Extract(context.Background(), path, TypeImage)
	if err == nil {
		t.Fatal("expected failure")
	}
	if got := err.Error(); got != "docling conversion failure: ocr failed" {
		t.Fatalf("error = %q", got)
	}
}

func TestDoclingExtractorPartialSuccessReturnsWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"document": map[string]any{"md_content": "partial", "text_content": "partial"},
			"status":   "partial_success",
			"errors":   []string{"page 2 failed"},
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := (DoclingExtractor{BaseURL: server.URL, Timeout: time.Minute}).Extract(context.Background(), path, TypePDF)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if result.Status != "partial_success" || !reflect.DeepEqual(result.Warnings, []string{"page 2 failed"}) {
		t.Fatalf("result = %+v", result)
	}
}
