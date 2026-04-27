package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type DoclingExtractor struct {
	BaseURL         string
	APIKey          string
	OutputFormats   []string
	DoOCR           bool
	ForceOCR        bool
	TableMode       string
	ImageExportMode string
	Timeout         time.Duration
	Client          *http.Client
}

type HealthStatus struct {
	OK         bool
	StatusCode int
	Detail     string
}

type doclingOCROptions struct {
	DoOCR    bool
	ForceOCR bool
}

func (e DoclingExtractor) Extract(ctx context.Context, path string, artifactType string) (Result, error) {
	if artifactType == TypeText {
		return extractTextFile(path)
	}
	if artifactType != TypePDF && artifactType != TypeImage {
		return Result{}, fmt.Errorf("unsupported artifact type %q", artifactType)
	}
	if e.BaseURL == "" {
		return Result{}, fmt.Errorf("docling base URL is required")
	}
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}

	options := e.ocrOptions(artifactType)
	result, err := e.extractOnce(ctx, path, artifactType, options)
	if err != nil {
		return Result{}, err
	}
	if shouldRetryWithForcedOCR(artifactType, options, result) {
		retryOptions := doclingOCROptions{DoOCR: true, ForceOCR: true}
		retry, retryErr := e.extractOnce(ctx, path, artifactType, retryOptions)
		if retryErr == nil {
			retry.Warnings = append([]string{"retried with forced OCR after low-text PDF extraction"}, retry.Warnings...)
			return retry, nil
		}
		result.Warnings = append(result.Warnings, "forced OCR retry failed: "+retryErr.Error())
	}
	return result, nil
}

func (e DoclingExtractor) extractOnce(ctx context.Context, path string, artifactType string, options doclingOCROptions) (Result, error) {
	requestBody, contentType, err := e.multipartBody(path, artifactType, options)
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(e.BaseURL, "/v1/convert/file"), requestBody)
	if err != nil {
		return Result{}, fmt.Errorf("create docling request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	if e.APIKey != "" {
		req.Header.Set("X-Api-Key", e.APIKey)
	}

	start := time.Now()
	resp, err := e.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("call docling: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read docling response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = resp.Status
		}
		return Result{}, fmt.Errorf("docling returned HTTP %d: %s", resp.StatusCode, message)
	}

	var converted doclingConvertResponse
	if err := json.Unmarshal(data, &converted); err != nil {
		return Result{}, fmt.Errorf("decode docling response: %w", err)
	}

	errorsOut := rawMessagesToStrings(converted.Errors)
	status := converted.Status
	if status == "" {
		status = "success"
	}
	if status == "failure" || status == "skipped" {
		return Result{}, fmt.Errorf("docling conversion %s: %s", status, strings.Join(errorsOut, "; "))
	}

	warnings := errorsOut
	if status == "partial_success" && len(warnings) == 0 {
		warnings = []string{"docling reported partial_success"}
	}

	return Result{
		Text:             converted.Document.Text,
		Markdown:         converted.Document.Markdown,
		StructuredJSON:   rawJSON(converted.Document.JSON),
		MetadataJSON:     metadataJSON(converted.Timings),
		Extractor:        "docling",
		ExtractorVersion: resp.Header.Get("X-Docling-Version"),
		Status:           status,
		ProcessingTime:   processingDuration(converted.ProcessingTime, elapsed),
		Warnings:         warnings,
		Errors:           errorsOut,
	}, nil
}

func (e DoclingExtractor) multipartBody(path string, artifactType string, options doclingOCROptions) (io.Reader, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("open file for docling: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string][]string{
		"from_formats":     {doclingFormat(artifactType)},
		"to_formats":       e.outputFormats(),
		"do_ocr":           {strconv.FormatBool(options.DoOCR)},
		"force_ocr":        {strconv.FormatBool(options.ForceOCR)},
		"abort_on_error":   {"false"},
		"document_timeout": {formatSeconds(e.Timeout)},
	}
	if e.TableMode != "" {
		fields["table_mode"] = []string{e.TableMode}
	}
	if e.ImageExportMode != "" {
		fields["image_export_mode"] = []string{e.ImageExportMode}
	}

	for name, values := range fields {
		for _, value := range values {
			if value == "" {
				continue
			}
			if err := writer.WriteField(name, value); err != nil {
				return nil, "", fmt.Errorf("write docling field %s: %w", name, err)
			}
		}
	}

	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "files",
		"filename": filepath.Base(path),
	}))
	header.Set("Content-Type", contentTypeForArtifact(path, artifactType))
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("create docling file part: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", fmt.Errorf("write docling file part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close docling multipart body: %w", err)
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType(), nil
}

func (e DoclingExtractor) ocrOptions(artifactType string) doclingOCROptions {
	options := doclingOCROptions{
		DoOCR:    e.DoOCR,
		ForceOCR: e.ForceOCR,
	}
	if artifactType == TypeImage {
		options.DoOCR = true
	}
	return options
}

func shouldRetryWithForcedOCR(artifactType string, options doclingOCROptions, result Result) bool {
	if artifactType != TypePDF || options.ForceOCR {
		return false
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		text = strings.TrimSpace(result.Markdown)
	}
	return len([]rune(text)) < 40
}

func (e DoclingExtractor) outputFormats() []string {
	if len(e.OutputFormats) > 0 {
		return e.OutputFormats
	}
	return []string{"md", "text", "json"}
}

func (e DoclingExtractor) httpClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

func CheckDoclingHealth(ctx context.Context, baseURL, apiKey string, timeout time.Duration) HealthStatus {
	if baseURL == "" {
		return HealthStatus{Detail: "not configured"}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var last HealthStatus
	for _, path := range []string{"/health", "/docs"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(baseURL, path), nil)
		if err != nil {
			return HealthStatus{Detail: err.Error()}
		}
		if apiKey != "" {
			req.Header.Set("X-Api-Key", apiKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return HealthStatus{Detail: err.Error()}
		}
		last = HealthStatus{StatusCode: resp.StatusCode, Detail: resp.Status}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return HealthStatus{OK: true, StatusCode: resp.StatusCode, Detail: resp.Status}
		}
	}
	return last
}

type doclingConvertResponse struct {
	Document       doclingDocument   `json:"document"`
	Status         string            `json:"status"`
	ProcessingTime float64           `json:"processing_time"`
	Timings        json.RawMessage   `json:"timings"`
	Errors         []json.RawMessage `json:"errors"`
}

type doclingDocument struct {
	Markdown string          `json:"md_content"`
	JSON     json.RawMessage `json:"json_content"`
	Text     string          `json:"text_content"`
}

func doclingFormat(artifactType string) string {
	if artifactType == TypeImage {
		return "image"
	}
	return artifactType
}

func contentTypeForArtifact(path string, artifactType string) string {
	switch artifactType {
	case TypePDF:
		return "application/pdf"
	case TypeImage:
		if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
			return contentType
		}
		return "image/*"
	default:
		return "application/octet-stream"
	}
}

func joinURL(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + path
}

func formatSeconds(timeout time.Duration) string {
	if timeout <= 0 {
		return ""
	}
	return strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64)
}

func rawJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	return string(trimmed)
}

func metadataJSON(timings json.RawMessage) string {
	if len(bytes.TrimSpace(timings)) == 0 {
		return ""
	}
	data, err := json.Marshal(map[string]json.RawMessage{"timings": timings})
	if err != nil {
		return ""
	}
	return string(data)
}

func processingDuration(seconds float64, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds * float64(time.Second))
}

func rawMessagesToStrings(messages []json.RawMessage) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		trimmed := bytes.TrimSpace(message)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		var text string
		if err := json.Unmarshal(trimmed, &text); err == nil {
			out = append(out, text)
			continue
		}
		out = append(out, string(trimmed))
	}
	return out
}
