package repository

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// The shared application data volume survives process and blue-green restarts.
// A ready file is a private write-ahead copy, not a customer download source.
type batchImageCheckpointSpool struct{ root string }

type batchImageCheckpointHeader struct {
	CustomID string
	MimeType string
	Width    int
	Height   int
	Size     int
}

const batchImageCheckpointReadySuffix = ".ready"

var batchImageCheckpointJobID = regexp.MustCompile(`^imgbatch_[a-zA-Z0-9_-]{1,55}$`)

func defaultBatchImageCheckpointSpool() *batchImageCheckpointSpool {
	base := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if base == "" {
		base = "data"
	}
	return &batchImageCheckpointSpool{root: filepath.Join(base, "batch-image-delivery-spool-v1")}
}

func (s *batchImageCheckpointSpool) directory(batchID, stage string) (string, error) {
	if !batchImageCheckpointJobID.MatchString(batchID) || batchImageCheckpointLimit(stage) == 0 {
		return "", errors.New("invalid image checkpoint identity")
	}
	return filepath.Join(s.root, batchID, stage), nil
}

func batchImageCheckpointLimit(stage string) int {
	switch stage {
	case service.BatchImageCheckpointSource:
		return 32 << 20
	case service.BatchImageCheckpointFinal:
		return 512 << 20
	default:
		return 0
	}
}

func batchImageCheckpointFilename(customID string) string {
	digest := sha256.Sum256([]byte(customID))
	return hex.EncodeToString(digest[:]) + batchImageCheckpointReadySuffix
}

func (s *batchImageCheckpointSpool) write(batchID, customID, stage string, value service.BatchImageResultCheckpoint) (string, bool, error) {
	dir, err := s.directory(batchID, stage)
	if err != nil {
		return "", false, err
	}
	if len(customID) == 0 || len(customID) > 1020 || len(value.Data) < 1 || len(value.Data) > batchImageCheckpointLimit(stage) || len(value.MimeType) > 128 {
		return "", false, errors.New("invalid image checkpoint size")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", false, err
	}
	f, err := os.CreateTemp(dir, ".writing-*")
	if err != nil {
		return "", false, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	header := batchImageCheckpointHeader{customID, value.MimeType, value.Width, value.Height, len(value.Data)}
	if err := json.NewEncoder(f).Encode(header); err != nil {
		return "", false, err
	}
	if _, err := f.Write(value.Data); err != nil {
		return "", false, err
	}
	if err := f.Sync(); err != nil {
		return "", false, err
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}
	ready := filepath.Join(dir, batchImageCheckpointFilename(customID))
	linkErr := os.Link(f.Name(), ready)
	if linkErr != nil && !errors.Is(linkErr, os.ErrExist) {
		return "", false, linkErr
	}
	// fsync the new directory entry and its parents before acknowledging the file.
	for _, parent := range []string{dir, filepath.Dir(dir), s.root, filepath.Dir(s.root)} {
		if err := syncBatchImageCheckpointDirectory(parent); err != nil {
			return ready, linkErr == nil, err
		}
	}
	return ready, linkErr == nil, nil
}

func syncBatchImageCheckpointDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func readBatchImageCheckpointFile(path, stage string) (string, *service.BatchImageResultCheckpoint, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReaderSize(f, 8<<10)
	line, err := reader.ReadSlice('\n')
	var header batchImageCheckpointHeader
	if err != nil || json.Unmarshal(line, &header) != nil || header.Size < 1 || header.Size > batchImageCheckpointLimit(stage) ||
		header.CustomID == "" || filepath.Base(path) != batchImageCheckpointFilename(header.CustomID) {
		return "", nil, errors.New("invalid image checkpoint file")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(line))+int64(header.Size) {
		return "", nil, errors.New("invalid image checkpoint file length")
	}
	data := make([]byte, header.Size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", nil, err
	}
	return header.CustomID, &service.BatchImageResultCheckpoint{
		Data: data, MimeType: header.MimeType, Width: header.Width, Height: header.Height,
	}, nil
}

func (s *batchImageCheckpointSpool) readyFiles(batchID, stage string) ([]string, error) {
	dir, err := s.directory(batchID, stage)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), batchImageCheckpointReadySuffix) {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files, nil
}

func removeBatchImageCheckpointFile(path string) {
	if path == "" || os.Remove(path) != nil {
		return
	}
	// Only remove empty directories; a concurrent writer can keep them alive.
	stageDir := filepath.Dir(path)
	_ = os.Remove(stageDir)
	_ = os.Remove(filepath.Dir(stageDir))
}
