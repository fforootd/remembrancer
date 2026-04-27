package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TypePDF   = "pdf"
	TypeImage = "image"
	TypeText  = "text"
)

type Result struct {
	Text             string
	Markdown         string
	StructuredJSON   string
	MetadataJSON     string
	Extractor        string
	ExtractorVersion string
	Status           string
	ProcessingTime   time.Duration
	Warnings         []string
	Errors           []string
}

type Extractor interface {
	Extract(ctx context.Context, path string, artifactType string) (Result, error)
}

type Router struct {
	Text   Extractor
	Binary Extractor
}

func (r Router) Extract(ctx context.Context, path string, artifactType string) (Result, error) {
	if artifactType == TypeText {
		if r.Text == nil {
			return Result{}, fmt.Errorf("text extractor is required")
		}
		return r.Text.Extract(ctx, path, artifactType)
	}
	if r.Binary == nil {
		return Result{}, fmt.Errorf("binary extractor is required")
	}
	return r.Binary.Extract(ctx, path, artifactType)
}

type LocalExtractor struct {
	Timeout time.Duration
}

func (e LocalExtractor) Extract(ctx context.Context, path string, artifactType string) (Result, error) {
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}

	switch artifactType {
	case TypeText:
		return extractTextFile(path)
	case TypePDF:
		return e.run(ctx, "pdftotext", []string{"-layout", path, "-"})
	case TypeImage:
		return e.run(ctx, "tesseract", []string{path, "stdout"})
	default:
		return Result{}, fmt.Errorf("unsupported artifact type %q", artifactType)
	}
}

func extractTextFile(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read text file: %w", err)
	}
	if !utf8.Valid(data) {
		return Result{}, errors.New("text file is not valid UTF-8")
	}
	return Result{
		Text:      string(data),
		Markdown:  string(data),
		Extractor: "utf8",
		Status:    "success",
	}, nil
}

func (e LocalExtractor) run(ctx context.Context, name string, args []string) (Result, error) {
	if _, err := exec.LookPath(name); err != nil {
		return Result{}, fmt.Errorf("%s is not installed or not on PATH: %w", name, err)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return Result{}, fmt.Errorf("%s failed: %s", name, message)
	}

	return Result{
		Text:      stdout.String(),
		Markdown:  stdout.String(),
		Extractor: name,
		Status:    "success",
	}, nil
}
