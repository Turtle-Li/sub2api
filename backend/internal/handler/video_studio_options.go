package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (q studioSubmission) validateMuseOptions(m service.VideoStudioModel) error {
	if q.NativeSize == "" && q.Resolution == "" && q.Timeout == nil && q.Extra == "" {
		return nil
	}
	v := m.MuseOptions
	if v == nil || m.ID != service.MuseVideoModel || len(q.NativeSize) > v.NativeSizeMaxBytes || (q.Size != "" && q.NativeSize != "") || len(q.Resolution) > v.ResolutionMaxBytes || len(q.Extra) > v.ExtraMaxBytes || (q.Timeout != nil && (*q.Timeout < 1 || *q.Timeout > v.TimeoutMax)) {
		return errors.New("此模型不支持所选高级参数或参数超出范围")
	}
	return nil
}

func studioMediaKind(data string) string {
	typ, _, _ := strings.Cut(data, ",")
	switch typ {
	case "data:image/jpeg;base64", "data:image/png;base64", "data:image/webp;base64":
		return "image"
	case "data:video/mp4;base64", "data:video/webm;base64":
		return "video"
	case "data:audio/mpeg;base64", "data:audio/wav;base64":
		return "audio"
	case "data:application/pdf;base64":
		return "file"
	default:
		return ""
	}
}

func validateStudioReference(ctx context.Context, value string, kinds []string) error {
	kind := studioMediaKind(value)
	if kind == "image" && (len(kinds) == 0 || slices.Contains(kinds, kind)) {
		return validateStudioImage(ctx, value)
	}
	invalid := errors.New("参考素材格式不受支持、内容不符或超过 8 MiB")
	if kind == "" || !slices.Contains(kinds, kind) {
		return invalid
	}
	head, s, _ := strings.Cut(value, ",")
	if len(s) > base64.StdEncoding.EncodedLen(studioRefLimit) {
		return invalid
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) == 0 || len(b) > studioRefLimit || !service.MatchesVideoStudioMediaType(b, strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")) {
		return invalid
	}
	return nil
}
