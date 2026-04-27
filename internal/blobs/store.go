package blobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const AlgorithmSHA256 = "sha256"

type Store struct {
	ArchiveRoot string
}

type Object struct {
	Hash        string
	Algorithm   string
	SizeBytes   int64
	StoragePath string
}

func HashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func (s Store) StoreFile(ctx context.Context, path string) (Object, error) {
	if s.ArchiveRoot == "" {
		return Object{}, fmt.Errorf("archive root is required")
	}

	if err := ctx.Err(); err != nil {
		return Object{}, err
	}

	tmpDir := filepath.Join(s.ArchiveRoot, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Object{}, fmt.Errorf("create blob tmp dir: %w", err)
	}

	source, err := os.Open(path)
	if err != nil {
		return Object{}, fmt.Errorf("open source file: %w", err)
	}
	defer source.Close()

	tmp, err := os.CreateTemp(tmpDir, "blob-*")
	if err != nil {
		return Object{}, fmt.Errorf("create blob temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	size, err := copyWithContext(ctx, io.MultiWriter(tmp, hasher), source)
	if closeErr := tmp.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return Object{}, fmt.Errorf("copy blob bytes: %w", err)
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	finalPath := s.PathForHash(hash)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return Object{}, fmt.Errorf("create blob object dir: %w", err)
	}

	if _, err := os.Stat(finalPath); err == nil {
		return Object{
			Hash:        hash,
			Algorithm:   AlgorithmSHA256,
			SizeBytes:   size,
			StoragePath: finalPath,
		}, nil
	} else if !os.IsNotExist(err) {
		return Object{}, fmt.Errorf("stat blob object: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return Object{}, fmt.Errorf("move blob into object store: %w", err)
	}

	return Object{
		Hash:        hash,
		Algorithm:   AlgorithmSHA256,
		SizeBytes:   size,
		StoragePath: finalPath,
	}, nil
}

func (s Store) PathForHash(hash string) string {
	return filepath.Join(s.ArchiveRoot, "objects", AlgorithmSHA256, hash[:2], hash[2:4], hash)
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}
