package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCanvasAllowsAPlanningChainAndImportedClipAsItsSource(t *testing.T) {
	clip, a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	doc := CanvasDocument{Viewport: CanvasViewport{Zoom: 1}, Nodes: []CanvasNode{
		{ID: clip, Kind: "video", VideoID: uuid.NewString()}, {ID: a, Kind: "video-draft"}, {ID: b, Kind: "video-draft"}, {ID: c, Kind: "video-draft"},
	}, Edges: []CanvasEdge{{Source: clip, Target: a}, {Source: a, Target: b}, {Source: b, Target: c}}}
	require.NoError(t, doc.Validate())
	doc.Edges = []CanvasEdge{{Source: c, Target: a}, {Source: a, Target: b}, {Source: b, Target: c}}
	require.ErrorIs(t, doc.Validate(), ErrCanvasInvalid)
}

func TestCanvasLineageStableSourcesAndInvalidConnections(t *testing.T) {
	image, root, a, b, note, raw := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	v := uuid.NewString()
	asset := &CanvasImage{Route: 0, Batch: "original", Item: "frame", Index: 3}
	d := CanvasDocument{Viewport: CanvasViewport{Zoom: 1}, Nodes: []CanvasNode{{ID: image, Kind: "image", Image: asset}, {ID: root, Kind: "video-draft", PinnedID: uuid.NewString()}, {ID: a, Kind: "video-draft"}, {ID: b, Kind: "video-draft"}, {ID: note, Kind: "note"}, {ID: raw, Kind: "video", VideoID: uuid.NewString()}}, Edges: []CanvasEdge{{Source: image, Target: root}, {Source: root, Target: a}, {Source: a, Target: b}, {Source: note, Target: b}}}
	require.NoError(t, d.Validate())
	g, err := d.Lineage()
	require.NoError(t, err)
	require.Equal(t, asset, g.Input[b].Image)
	require.Empty(t, g.Input[b].VideoID)
	require.False(t, g.Continuation[root])
	require.True(t, g.Continuation[b])
	d.Edges[1].TakeID = v
	g, err = d.Lineage()
	require.NoError(t, err)
	require.Nil(t, g.Input[b].Image)
	require.Equal(t, v, g.Input[b].VideoID)
	for name, edge := range map[string]CanvasEdge{"two sources": {Source: raw, Target: b}, "cycle even when pinned": {Source: b, Target: root, TakeID: v}, "repeated different take": {Source: root, Target: a, TakeID: uuid.NewString()}, "annotation with take": {Source: note, Target: a, TakeID: v}, "wrong destination": {Source: root, Target: image}, "missing source": {Source: uuid.NewString(), Target: b}} {
		t.Run(name, func(t *testing.T) {
			x := d
			x.Edges = append(append([]CanvasEdge{}, d.Edges...), edge)
			require.ErrorIs(t, x.Validate(), ErrCanvasInvalid)
		})
	}
	d.Edges = []CanvasEdge{{Source: root, Target: a}, {Source: a, Target: b}, {Source: b, Target: root, TakeID: v}}
	require.ErrorIs(t, d.Validate(), ErrCanvasInvalid)
	d.Edges = []CanvasEdge{{Source: image, Target: b, TakeID: v}}
	require.ErrorIs(t, d.Validate(), ErrCanvasInvalid)
	d.Edges = []CanvasEdge{{Source: raw, Target: b, TakeID: v}}
	require.ErrorIs(t, d.Validate(), ErrCanvasInvalid)
	d.Edges = []CanvasEdge{{Source: root, Target: a}, {Source: a, Target: b}}
	g, err = d.Lineage()
	require.NoError(t, err)
	require.Equal(t, CanvasInput{}, g.Input[b], "a planning branch without assets is text, not its parent's preview")
}
