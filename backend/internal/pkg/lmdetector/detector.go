package lmdetector

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"sync"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
)

//go:embed data
var bundledData embed.FS

var load = sync.OnceValues(loadBundledDetector)

func Load() (*lmfpd.Detector, error) {
	return load()
}

func loadBundledDetector() (*lmfpd.Detector, error) {
	assets, err := fs.Sub(bundledData, "data")
	if err != nil {
		return nil, fmt.Errorf("open LM Detector assets: %w", err)
	}
	stagingDir, err := os.MkdirTemp("", "sub2api-lm-detector-")
	if err != nil {
		return nil, fmt.Errorf("prepare LM Detector local data: %w", err)
	}
	defer os.RemoveAll(stagingDir)
	if err := os.CopyFS(stagingDir, assets); err != nil {
		return nil, fmt.Errorf("stage LM Detector local data: %w", err)
	}
	return lmfpd.Load(context.Background(), lmfpd.LoadOptions{Sources: []string{stagingDir}})
}
