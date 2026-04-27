package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"path/filepath"
	"strings"

	"zora/internal/extract"
)

const JobKindIngestFile = "ingest.file"

type FilePayload struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	SourceID    string `json:"source_id"`
	SizeBytes   int64  `json:"size_bytes"`
	MTime       string `json:"mtime"`
	Type        string `json:"type"`
	MIMEType    string `json:"mime_type"`
	Title       string `json:"title"`
}

func DetectFile(path string) (artifactType, mimeType string, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return extract.TypePDF, "application/pdf", true
	case ".png":
		return extract.TypeImage, "image/png", true
	case ".jpg", ".jpeg":
		return extract.TypeImage, "image/jpeg", true
	case ".tif", ".tiff":
		return extract.TypeImage, "image/tiff", true
	case ".txt", ".md":
		mimeType = mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "text/plain; charset=utf-8"
		}
		return extract.TypeText, mimeType, true
	default:
		return "", "", false
	}
}

func SourceID(absPath, contentHash string) string {
	sum := sha256.Sum256([]byte(absPath + "\x00" + contentHash))
	return "watch_folder:" + hex.EncodeToString(sum[:])
}

func ArtifactID(sourceID string) string {
	sum := sha256.Sum256([]byte(sourceID))
	return "art_" + hex.EncodeToString(sum[:16])
}

func TitleFromPath(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}
