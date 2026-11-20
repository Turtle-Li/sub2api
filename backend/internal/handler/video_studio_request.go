package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

const VideoStudioBodyLimit = 24 << 20
const studioRefLimit = 8 << 20
const studioImageMaxPixels = 4096 * 4096

type studioReferences []string

// Do not allocate a slice proportional to an untrusted JSON array; the directory sets a hard cap.
func (r *studioReferences) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if tok, err := d.Token(); err != nil || tok != json.Delim('[') {
		return errors.New("参考图须为数组")
	}
	for d.More() {
		if len(*r) >= service.VideoStudioMaxReferences {
			return errors.New("最多可使用十个参考素材")
		}
		var image string
		if err := d.Decode(&image); err != nil {
			return err
		}
		if image == "" {
			return errors.New("参考素材不能为空")
		}
		*r = append(*r, image)
	}
	_, err := d.Token()
	return err
}

type studioSubmission struct {
	ID         string           `json:"request_id"`
	Model      string           `json:"model"`
	Quote      string           `json:"quote_id"`
	Prompt     string           `json:"prompt"`
	Seconds    int              `json:"seconds"`
	Size       string           `json:"size,omitempty"`
	Seed       *int64           `json:"seed,omitempty"`
	First      string           `json:"first_frame,omitempty"`
	Last       string           `json:"last_frame,omitempty"`
	References studioReferences `json:"references,omitempty"`
	NativeSize string           `json:"native_size,omitempty"`
	Resolution string           `json:"resolution,omitempty"`
	Timeout    *int             `json:"timeout,omitempty"`
	Extra      string           `json:"extra,omitempty"`
}

func (q studioSubmission) parameters() *service.VideoStudioParameters {
	mode := "text"
	switch {
	case q.Last != "":
		mode = "first_last_frame"
	case q.First != "":
		mode = "first_frame"
	case len(q.References) > 0:
		mode = "references"
	}
	var kinds []string
	for _, v := range q.References {
		kinds = append(kinds, studioMediaKind(v))
	}
	p := &service.VideoStudioParameters{MaterialMode: mode, ReferenceCount: len(q.References), Seed: q.Seed, ReferenceKinds: kinds, NativeSize: q.NativeSize, Resolution: q.Resolution, Timeout: q.Timeout, HasExtra: q.Extra != ""}
	if q.Model == service.MuseVideoModel {
		p.DurationMode = service.VideoStudioDurationAutomatic
	} else if service.IsGoodPutVideoModel(q.Model) {
		p.DurationMode = service.VideoStudioDurationSelectable
	}
	return p
}

func validStudioID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}
func decodeStudioSubmission(raw []byte) (studioSubmission, error) {
	var q studioSubmission
	bad := errors.New("视频请求字段无效或重复")
	if _, yes := requestmodel.ConflictingField("application/json", raw); yes {
		return q, bad
	}
	if !utf8.Valid(raw) {
		return q, bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	input := struct {
		*studioSubmission
		Seconds json.RawMessage `json:"seconds"`
	}{studioSubmission: &q}
	if err := d.Decode(&input); err != nil || d.Decode(new(json.RawMessage)) != io.EOF {
		return q, bad
	}
	// Normalize an absent Muse duration before fingerprinting. Explicit historical seconds
	// keep the original fingerprint for receipt lookups; only new dispatches use today's catalogue.
	if input.Seconds == nil && q.Model == service.MuseVideoModel {
		q.Seconds = service.MuseVideoDefaultSeconds
	} else if len(input.Seconds) == 0 || json.Unmarshal(input.Seconds, &q.Seconds) != nil || q.Seconds < 1 {
		return q, errors.New("时长字段无效：Muse 由渠道决定，其他模型须提交正整数秒数")
	}
	q.Prompt = strings.TrimSpace(q.Prompt)
	if !validStudioID(q.ID) || q.Prompt == "" || !utf8.ValidString(q.Prompt) || len(q.Prompt) > 8192 || len(q.Quote) != 64 || (!service.IsGoodPutVideoModel(q.Model) && q.Model != service.MuseVideoModel) {
		return q, errors.New("请检查模型、提示词或任务标识并刷新报价")
	}
	return q, nil
}
func (q studioSubmission) fingerprint() string {
	b, _ := json.Marshal(q)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validateStudioImage(ctx context.Context, s string) error {
	invalid := errors.New("请使用可解码的 PNG、JPEG 或 WebP 原图，单张不超过 8 MiB 和 1678 万像素")
	typ, data, ok := strings.Cut(s, ",")
	if !ok {
		return invalid
	}
	mime, ok := strings.CutPrefix(typ, "data:")
	if !ok {
		return invalid
	}
	mime, ok = strings.CutSuffix(mime, ";base64")
	if !ok || !slices.Contains([]string{"image/png", "image/jpeg", "image/webp"}, mime) || len(data) > base64.StdEncoding.EncodedLen(studioRefLimit) {
		return invalid
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(decoded) == 0 || len(decoded) > studioRefLimit {
		return invalid
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || "image/"+format != mime || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > studioImageMaxPixels/cfg.Height {
		return invalid
	}
	// One raster at a time; reserve decoder scratch as well as the largest pixel representation.
	n := int64(cfg.Width) * int64(cfg.Height) * 16
	if err := httputil.ReserveRequestBodyMemory(ctx, n); err != nil {
		return err
	}
	defer httputil.ReleaseRequestBodyMemory(ctx, n)
	if _, _, err := image.Decode(bytes.NewReader(decoded)); err != nil {
		return invalid
	}
	return nil
}
func (q studioSubmission) providerBody(ctx context.Context, m service.VideoStudioModel) ([]byte, error) {
	if q.Model == service.MuseVideoModel && q.Seconds != service.MuseVideoDefaultSeconds {
		return nil, errors.New("Muse 成片时长由渠道决定，不能自定义秒数")
	}
	if q.Model != m.ID || q.Seconds < m.MinSeconds || q.Seconds > m.MaxSeconds || (q.Size != "" && !slices.Contains(m.Sizes, q.Size)) {
		return nil, errors.New("此模型不支持所选时长或画幅")
	}
	if len(q.References) > m.MaxReferences || q.Last != "" && q.First == "" || (q.First != "" || q.Last != "") && (!m.SupportsFrames || len(q.References) > 0) {
		return nil, errors.New("请使用首帧和可选尾帧，或单独使用参考图")
	}
	if q.Seed != nil && (!m.SupportsSeed || *q.Seed < 0 || *q.Seed > 2147483647) {
		return nil, errors.New("此模型不支持所选随机种子")
	}
	if err := q.validateMuseOptions(m); err != nil {
		return nil, err
	}
	for _, v := range q.References {
		if err := validateStudioReference(ctx, v, m.ReferenceKinds); err != nil {
			return nil, err
		}
	}
	for _, v := range []string{q.First, q.Last} {
		if v != "" {
			if err := validateStudioImage(ctx, v); err != nil {
				return nil, err
			}
		}
	}
	body := map[string]any{"model": q.Model, "prompt": q.Prompt}
	if q.Model == service.MuseVideoModel {
		// Omit duration. Muse's adapter applies its own default; the wallet uses that same fixed basis.
		if q.NativeSize != "" {
			body["size"] = q.NativeSize
		}
		if q.Resolution != "" {
			body["resolution"] = q.Resolution
		}
		if q.Timeout != nil {
			body["timeout"] = *q.Timeout
		}
		if q.Extra != "" {
			body["extra"] = q.Extra
		}
		if q.Size != "" {
			body["aspect_ratio"] = q.Size
		}
		if len(q.References) > 0 {
			body["images"] = q.References
		}
	} else {
		body["seconds"] = q.Seconds
		if q.Size != "" {
			body["size"] = q.Size
		}
		if q.Seed != nil {
			body["seed"] = *q.Seed
		}
		type media struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		var images []media
		if q.First != "" {
			images = append(images, media{"first_frame", q.First})
		}
		if q.Last != "" {
			images = append(images, media{"last_frame", q.Last})
		}
		for _, v := range q.References {
			images = append(images, media{"reference_" + studioMediaKind(v), v})
		}
		if len(images) > 0 {
			body["media"] = images
		}
	}
	b, err := json.Marshal(body)
	if service.IsGoodPutVideoModel(q.Model) && len(b) > service.GoodPutVideoRequestMaxBytes {
		return nil, errors.New("H3 的整个请求不能超过 8 MiB，请缩小图片")
	}
	return b, err
}
