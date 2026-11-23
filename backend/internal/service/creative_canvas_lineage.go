package service

// A missing media input is a text-planning shot, never a request to use the parent's latest take.
type CanvasInput struct {
	Image   *CanvasImage `json:"image,omitempty"`
	VideoID string       `json:"video_id,omitempty"`
}

func (a CanvasInput) Equal(b CanvasInput) bool {
	if a.VideoID != b.VideoID || (a.Image == nil) != (b.Image == nil) {
		return false
	}
	return a.Image == nil || *a.Image == *b.Image
}

// Returned for continuation nodes so clients don't have to guess which private asset to read.
type CanvasResolvedInput struct {
	CanvasInput
	Available bool `json:"available"`
}

type CanvasLineage struct {
	Parent       map[string]CanvasEdge
	Input        map[string]CanvasInput
	Continuation map[string]bool
}

const CanvasContinuationVersion = 2

// A v1 editor silently drops continuation edges when loading a board. It may read, not replace, that graph.
func CheckCanvasGraphVersion(version int, needsContinuation bool) error {
	if version < 0 || version > CanvasContinuationVersion {
		return ErrCanvasInvalid
	}
	if needsContinuation && version < CanvasContinuationVersion {
		return ErrCanvasConflict
	}
	return nil
}

// Lineage resolves each node once. Notes are annotations, not media parents. A pinned edge still
// participates in cycle detection; pinning a take must not make a back-edge legal.
func (d CanvasDocument) Lineage() (CanvasLineage, error) {
	g := CanvasLineage{Parent: make(map[string]CanvasEdge), Input: make(map[string]CanvasInput), Continuation: make(map[string]bool)}
	if len(d.Nodes) > CanvasMaxNodes || len(d.Edges) > CanvasMaxEdges {
		return g, ErrCanvasInvalid
	}
	nodes := make(map[string]CanvasNode, len(d.Nodes))
	for _, n := range d.Nodes {
		nodes[n.ID] = n
	}
	seen := make(map[[2]string]bool, len(d.Edges))
	for _, e := range d.Edges {
		key := [2]string{e.Source, e.Target}
		src, okS := nodes[e.Source]
		dst, okT := nodes[e.Target]
		if !okS || !okT || e.Source == e.Target || seen[key] {
			return g, ErrCanvasInvalid
		}
		seen[key] = true
		if src.Kind == "note" && (dst.Kind == "image" || dst.Kind == "video-draft") && e.TakeID == "" {
			continue
		}
		if dst.Kind != "video-draft" || (src.Kind != "image" && src.Kind != "video" && src.Kind != "video-draft") {
			return g, ErrCanvasInvalid
		}
		if e.TakeID != "" && (src.Kind != "video-draft" || !CanvasID(e.TakeID)) {
			return g, ErrCanvasInvalid
		}
		if _, exists := g.Parent[e.Target]; exists {
			return g, ErrCanvasInvalid
		}
		g.Parent[e.Target] = e
		if src.Kind != "image" {
			g.Continuation[e.Target] = true
		}
	}
	visiting := make(map[string]bool, len(d.Nodes))
	var visit func(string) error
	visit = func(id string) error {
		if _, ok := g.Input[id]; ok {
			return nil
		}
		if visiting[id] {
			return ErrCanvasInvalid
		}
		visiting[id] = true
		n := nodes[id]
		input := CanvasInput{Image: n.Image, VideoID: n.VideoID}
		if e, ok := g.Parent[id]; ok {
			if err := visit(e.Source); err != nil {
				return err
			}
			input = g.Input[e.Source]
			if e.TakeID != "" {
				input = CanvasInput{VideoID: e.TakeID}
			}
		}
		delete(visiting, id)
		g.Input[id] = input
		return nil
	}
	for _, n := range d.Nodes {
		if err := visit(n.ID); err != nil {
			return g, err
		}
	}
	return g, nil
}
