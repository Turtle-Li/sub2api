package service

import (
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCanvasGraphRulesKeepIndependentImageBranches(t *testing.T) {
	im, a, b, n := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	src := CanvasImage{Batch: "imgbatch_123", Item: "frame-1"}
	doc := func() CanvasDocument {
		return CanvasDocument{Viewport: CanvasViewport{Zoom: 1}, Nodes: []CanvasNode{{ID: im, Kind: "image", Image: &src}, {ID: a, Kind: "video-draft"}, {ID: b, Kind: "video-draft"}, {ID: n, Kind: "note"}}, Edges: []CanvasEdge{{im, a}, {im, b}, {n, im}}}
	}
	require.NoError(t, doc().Validate())
	require.Equal(t, &src, doc().SourceImage(b))
	for name, f := range map[string]func(*CanvasDocument){"duplicate": func(d *CanvasDocument) { d.Nodes[1].ID = im }, "dangling": func(d *CanvasDocument) { d.Edges[0].Source = uuid.NewString() }, "self": func(d *CanvasDocument) { d.Edges[0].Source = a }, "reversed": func(d *CanvasDocument) { d.Edges[1] = CanvasEdge{a, im} }, "duplicate edge": func(d *CanvasDocument) { d.Edges[1] = d.Edges[0] }, "different original": func(d *CanvasDocument) {
		s := d.Nodes[0]
		s.ID = uuid.NewString()
		d.Nodes = append(d.Nodes, s)
		d.Edges = append(d.Edges, CanvasEdge{s.ID, a})
	}, "NaN": func(d *CanvasDocument) { d.Nodes[0].X = math.NaN() }, "oversize zoom": func(d *CanvasDocument) { d.Viewport.Zoom = 20 }, "wrong asset": func(d *CanvasDocument) { d.Nodes[1].Image = &src }, "outside segment": func(d *CanvasDocument) { s := src; s.Item = "https://example.invalid"; d.Nodes[0].Image = &s }, "null array": func(d *CanvasDocument) { d.Nodes = nil }, "oversize": func(d *CanvasDocument) { d.Nodes = make([]CanvasNode, CanvasMaxNodes+1) }} {
		t.Run(name, func(t *testing.T) { v := doc(); f(&v); require.ErrorIs(t, v.Validate(), ErrCanvasInvalid) })
	}
}
