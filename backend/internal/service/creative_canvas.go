package service

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	CanvasMaxNodes         = 160
	CanvasMaxEdges         = 320
	CanvasMaxBoards        = 32
	CanvasMaxAttempts      = 256
	CanvasMaxDraftAttempts = 48
	CanvasBodyLimit        = 512 << 10
)

var (
	ErrCanvasInvalid  = errors.New("画布内容无效")
	ErrCanvasNotFound = errors.New("画布不存在或不可访问")
	ErrCanvasConflict = errors.New("画布已变化，请重新加载后再保存")
	ErrCanvasLimit    = errors.New("已达画布容量限制")
	ErrCanvasAsset    = errors.New("作品不可用，请重新选择本人的作品")
	canvasSegment     = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)
)

type CanvasViewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}
type CanvasImage struct {
	Route int64  `json:"route"`
	Batch string `json:"batchId"`
	Item  string `json:"itemId"`
	Index int    `json:"index"`
}
type CanvasNode struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	X        float64      `json:"x"`
	Y        float64      `json:"y"`
	Title    string       `json:"title,omitempty"`
	Prompt   string       `json:"prompt,omitempty"`
	Image    *CanvasImage `json:"image,omitempty"`
	VideoID  string       `json:"video_id,omitempty"`
	PinnedID string       `json:"pinned_id,omitempty"`
}
type CanvasEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}
type CanvasDocument struct {
	Viewport CanvasViewport `json:"viewport"`
	Nodes    []CanvasNode   `json:"nodes"`
	Edges    []CanvasEdge   `json:"edges"`
}
type CanvasSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}
type CanvasAsset struct {
	Available bool            `json:"available"`
	Job       *VideoStudioJob `json:"job,omitempty"`
}
type CanvasAttempt struct {
	Unavailable bool            `json:"unavailable,omitempty"`
	NodeID      string          `json:"node_id"`
	RequestID   string          `json:"request_id"`
	CreatedAt   time.Time       `json:"created_at"`
	Job         *VideoStudioJob `json:"job,omitempty"`
}
type CreativeCanvas struct {
	CanvasSummary
	Document CanvasDocument         `json:"document"`
	Assets   map[string]CanvasAsset `json:"assets"`
	Attempts []CanvasAttempt        `json:"attempts"`
}
type CanvasUpdate struct {
	Title    string         `json:"title"`
	Revision int64          `json:"revision"`
	Document CanvasDocument `json:"document"`
}

// The canvas store has no submission, provider-poll, media-download or billing capabilities.
type CreativeCanvasStore interface {
	List(context.Context, int64) ([]CanvasSummary, error)
	Create(context.Context, int64, string) (CreativeCanvas, error)
	Get(context.Context, int64, string) (CreativeCanvas, error)
	Save(context.Context, int64, string, CanvasUpdate) error
	Remove(context.Context, int64, string, int64) error
	Bind(context.Context, int64, string, string, string, int64) error
	ResolveImages(context.Context, int64, []CanvasImage) (map[CanvasImage]bool, error)
	ReadJobs(context.Context, int64, []string) ([]VideoStudioJob, error)
}

func CanvasID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u != uuid.Nil && s == u.String()
}
func canvasText(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' }) < 0
}
func CanvasTitle(s string) bool  { return strings.TrimSpace(s) != "" && canvasText(s, 100) }
func canvasCoord(f float64) bool { return !math.IsNaN(f) && math.Abs(f) <= 1e7 }
func (d CanvasDocument) Validate() error {
	if d.Nodes == nil || d.Edges == nil || len(d.Nodes) > CanvasMaxNodes || len(d.Edges) > CanvasMaxEdges || !canvasCoord(d.Viewport.X) || !canvasCoord(d.Viewport.Y) || !canvasCoord(d.Viewport.Zoom) || d.Viewport.Zoom < 0.05 || d.Viewport.Zoom > 8 {
		return ErrCanvasInvalid
	}
	kinds := make(map[string]string, len(d.Nodes))
	for _, n := range d.Nodes {
		if !CanvasID(n.ID) || kinds[n.ID] != "" || !canvasCoord(n.X) || !canvasCoord(n.Y) || !canvasText(n.Title, 120) || !canvasText(n.Prompt, 4000) {
			return ErrCanvasInvalid
		}
		kinds[n.ID] = n.Kind
		if (n.Kind == "image") != (n.Image != nil) || (n.Kind == "video") != (n.VideoID != "") || (n.PinnedID != "" && (n.Kind != "video-draft" || !CanvasID(n.PinnedID))) {
			return ErrCanvasInvalid
		}
		switch n.Kind {
		case "image":
			i := n.Image
			if i.Route < 0 || i.Route > 1<<53-1 || i.Index < 0 || i.Index > 4095 || !canvasSegment.MatchString(i.Batch) || !canvasSegment.MatchString(i.Item) {
				return ErrCanvasInvalid
			}
		case "video":
			if !CanvasID(n.VideoID) {
				return ErrCanvasInvalid
			}
		case "video-draft", "note":
		default:
			return ErrCanvasInvalid
		}
	}
	// Directed type rules form a DAG by construction. One image can feed many drafts, but a draft has one first image.
	seen := map[CanvasEdge]bool{}
	parent := map[string]bool{}
	for _, e := range d.Edges {
		s, t := kinds[e.Source], kinds[e.Target]
		if seen[e] || !(s == "image" && t == "video-draft" || s == "note" && (t == "image" || t == "video-draft")) {
			return ErrCanvasInvalid
		}
		seen[e] = true
		if s == "image" {
			if parent[e.Target] {
				return ErrCanvasInvalid
			}
			parent[e.Target] = true
		}
	}
	return nil
}
func (d CanvasDocument) Images() map[string]CanvasImage {
	m := map[string]CanvasImage{}
	for _, n := range d.Nodes {
		if n.Image != nil {
			m[n.ID] = *n.Image
		}
	}
	return m
}
func (d CanvasDocument) SourceImage(node string) *CanvasImage {
	images := d.Images()
	for _, e := range d.Edges {
		if e.Target == node {
			if i, ok := images[e.Source]; ok {
				return &i
			}
		}
	}
	return nil
}
