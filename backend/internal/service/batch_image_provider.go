package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type BatchImageProvider interface {
	Name() string
	SupportsAccount(account *Account) bool
	Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error)
	Get(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error)
	Cancel(ctx context.Context, job *BatchImageJob, account *Account) error
	OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error)
	Cleanup(ctx context.Context, job *BatchImageJob, account *Account, target CleanupTarget) error
}

type BatchImageProviderRegistry struct {
	providers map[string]BatchImageProvider
}

func NewBatchImageProviderRegistry(providers ...BatchImageProvider) *BatchImageProviderRegistry {
	r := &BatchImageProviderRegistry{providers: make(map[string]BatchImageProvider, len(providers))}
	for _, provider := range providers {
		if provider == nil || strings.TrimSpace(provider.Name()) == "" {
			continue
		}
		r.providers[provider.Name()] = provider
	}
	return r
}

func NewDefaultBatchImageProviderRegistry() *BatchImageProviderRegistry {
	return NewBatchImageProviderRegistry(
		NewGeminiAPIBatchImageProvider(nil),
		NewVertexBatchImageProvider(VertexBatchImageProviderOptions{}, nil, nil, nil),
	)
}

func NewBatchImageProviderRegistryFromConfig(cfg *config.Config) *BatchImageProviderRegistry {
	return NewBatchImageProviderRegistry(
		NewGeminiAPIBatchImageProvider(nil),
		NewVertexBatchImageProviderFromConfig(cfg, nil, nil, nil),
	)
}

func (r *BatchImageProviderRegistry) Get(provider string) (BatchImageProvider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.providers[provider]
	return p, ok
}

func (r *BatchImageProviderRegistry) MustGet(provider string) (BatchImageProvider, error) {
	p, ok := r.Get(provider)
	if !ok {
		return nil, ErrBatchImageInvalidProvider
	}
	return p, nil
}

type BatchImageInput struct {
	BatchID     string
	Model       string
	DisplayName string
	Items       []BatchImageInputItem

	ResponseMimeType string
	AspectRatio      string
	ImageSize        string

	Metadata map[string]string
}

type BatchImageInputItem struct {
	CustomID string
	Prompt   string

	ReferenceImages []BatchImageReference
}

type BatchImageReference struct {
	ID       string
	Type     string
	MimeType string
	Data     []byte
	FileURI  string
}

// batchImageReferenceRoleGuide keeps typed reference authority adjacent to the
// image part that it governs. Empty types retain the legacy public API behavior;
// non-empty unknown types fail closed without echoing caller-controlled text.
func batchImageReferenceRoleGuide(referenceType string) (string, bool) {
	typeName := strings.ToUpper(strings.TrimSpace(referenceType))
	if typeName == "" {
		return "", false
	}
	prefix := "REFERENCE_IMAGE_ROLE=" + typeName + ". The immediately following image "
	switch typeName {
	case "PRODUCT_TRUTH", "PRODUCT_DETAIL":
		return prefix + "is authoritative only for the product's visible identity, geometry, color, pattern, material, logo, text, and construction. Ignore its background, people, body pose, props, framing, and unrelated objects.", true
	case "LOGO_REFERENCE":
		return prefix + "is authoritative only for the supplied logo and allowed brand text. It grants no product, person, scene, composition, or style authority.", true
	case "MODEL_REFERENCE":
		return prefix + "is authoritative only for the adult person's identity, anatomy, and prompt-allowed pose or interaction. Every product, garment, accessory, package, logo, text, color, material, pattern, texture, silhouette, or construction shown on it is an untrusted placeholder. Discard that placeholder completely and fully replace every worn, held, overlapping, or target-position item with PRODUCT_TRUTH.", true
	case "SCENE_REFERENCE":
		return prefix + "may contribute only prompt-allowed environment, composition, camera, and lighting. Ignore people, body poses, clothing, carried objects, and competing products; it grants no product identity authority.", true
	case "STYLE_REFERENCE":
		return prefix + "may contribute only prompt-allowed visual style, lighting, tone, and texture treatment. Ignore its subjects, people, products, text, logo, geometry, and composition unless separately authorized.", true
	case "EDIT_TARGET":
		return prefix + "is the authoritative source canvas. Preserve all pixels and relationships outside the explicitly requested edit target.", true
	case "EDIT_REFERENCE":
		return prefix + "may contribute only the visual change explicitly assigned by the prompt. It grants no authority over unrelated product, person, scene, or canvas facts.", true
	case "EDIT_MASK":
		return prefix + "is target-region evidence only. Treat it as a mask, never as visible output content or product truth.", true
	case "CONTINUITY_REFERENCE":
		return prefix + "may contribute only the continuity facts explicitly assigned by the prompt. It grants no independent product identity authority.", true
	default:
		return "REFERENCE_IMAGE_ROLE=UNRECOGNIZED. The immediately following image has no independent authority. Do not derive product identity or locked facts from it.", true
	}
}

type BatchProviderJob struct {
	ProviderJobName   string
	ProviderInputRef  string
	ProviderOutputRef string
	RawState          string
}

type BatchProviderInternalState string

const (
	BatchProviderStateQueued    BatchProviderInternalState = "queued"
	BatchProviderStateRunning   BatchProviderInternalState = "running"
	BatchProviderStateSucceeded BatchProviderInternalState = "succeeded"
	BatchProviderStateFailed    BatchProviderInternalState = "failed"
	BatchProviderStateCancelled BatchProviderInternalState = "cancelled"
	BatchProviderStateExpired   BatchProviderInternalState = "expired"
)

type BatchProviderStatus struct {
	RawState string

	InternalState BatchProviderInternalState
	Done          bool

	ProviderOutputRef string
	SuccessfulCount   *int
	FailedCount       *int
	IncompleteCount   *int

	ErrorCode    string
	ErrorMessage string

	SuggestedRequeueAfter time.Duration
}

type CleanupTarget string

const (
	CleanupTargetInput  CleanupTarget = "input"
	CleanupTargetOutput CleanupTarget = "output"
	CleanupTargetAll    CleanupTarget = "all"
)

var (
	ErrBatchImageProviderUnsupportedAccount      = infraerrors.New(http.StatusBadRequest, "BATCH_IMAGE_PROVIDER_UNSUPPORTED_ACCOUNT", "batch image provider does not support this account")
	ErrBatchImageProviderMissingAPIKey           = infraerrors.New(http.StatusBadRequest, "BATCH_IMAGE_PROVIDER_MISSING_API_KEY", "batch image provider account is missing api key")
	ErrBatchImageProviderMissingServiceAccount   = infraerrors.New(http.StatusBadRequest, "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT", "batch image provider account is missing service account credentials")
	ErrBatchImageProviderMissingJobName          = infraerrors.New(http.StatusBadRequest, "BATCH_IMAGE_PROVIDER_MISSING_JOB_NAME", "batch image provider job name is missing")
	ErrBatchImageProviderMissingResultRef        = infraerrors.New(http.StatusBadRequest, "BATCH_IMAGE_PROVIDER_MISSING_RESULT_REF", "batch image provider result reference is missing")
	ErrBatchImageProviderInlineResultUnsupported = infraerrors.New(http.StatusBadRequest, "GEMINI_INLINE_BATCH_RESULT_UNSUPPORTED", "Gemini inline batch result is not supported")
	ErrBatchImageProviderInvalidInput            = infraerrors.New(http.StatusBadRequest, "BATCH_IMAGE_PROVIDER_INVALID_INPUT", "invalid batch image provider input")
	ErrBatchImageProviderUnsafeCleanupPath       = infraerrors.New(http.StatusBadRequest, "VERTEX_UNSAFE_CLEANUP_PATH", "unsafe batch image cleanup path")
	ErrUnsupportedCleanupTarget                  = infraerrors.New(http.StatusBadRequest, "BATCH_IMAGE_PROVIDER_UNSUPPORTED_CLEANUP_TARGET", "unsupported batch image cleanup target")
)

func batchImageProviderJobName(job *BatchImageJob) string {
	if job == nil || job.ProviderJobName == nil {
		return ""
	}
	return strings.TrimSpace(*job.ProviderJobName)
}

func batchImageProviderInputRef(job *BatchImageJob) string {
	if job == nil || job.ProviderInputRef == nil {
		return ""
	}
	return strings.TrimSpace(*job.ProviderInputRef)
}

func batchImageProviderOutputRef(job *BatchImageJob) string {
	if job == nil || job.ProviderOutputRef == nil {
		return ""
	}
	return strings.TrimSpace(*job.ProviderOutputRef)
}

func batchImageProviderAPIKey(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("api_key"))
}

func batchImageProviderInputError(format string, args ...any) error {
	return ErrBatchImageProviderInvalidInput.WithCause(fmt.Errorf(format, args...))
}
