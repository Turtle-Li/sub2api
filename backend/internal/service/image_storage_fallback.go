package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

func logImageStorageFallback(path string, imageCount int, err error) {
	failureType := "unknown"
	if err != nil {
		failureType = strings.TrimPrefix(fmt.Sprintf("%T", err), "*")
	}
	logger.L().Warn("image_generation.object_storage_inline_fallback",
		zap.String("path", strings.TrimSpace(path)),
		zap.Int("image_count", imageCount),
		zap.String("failure_type", failureType),
	)
}
