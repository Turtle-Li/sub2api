package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type creativeCanvasRepository struct{ db *sql.DB }

func NewCreativeCanvasRepository(db *sql.DB) service.CreativeCanvasStore {
	return &creativeCanvasRepository{db}
}

const boardFields = `id::text,title,revision,updated_at`

func (r *creativeCanvasRepository) List(ctx context.Context, u int64) ([]service.CanvasSummary, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+boardFields+` FROM creative_canvases WHERE user_id=$1 ORDER BY updated_at DESC,id`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []service.CanvasSummary{}
	for rows.Next() {
		var b service.CanvasSummary
		if err = rows.Scan(&b.ID, &b.Title, &b.Revision, &b.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, b)
	}
	return items, rows.Err()
}
func emptyBoard() service.CreativeCanvas {
	return service.CreativeCanvas{Document: service.CanvasDocument{Viewport: service.CanvasViewport{Zoom: 1}, Nodes: []service.CanvasNode{}, Edges: []service.CanvasEdge{}}, Assets: map[string]service.CanvasAsset{}, Attempts: []service.CanvasAttempt{}}
}
func (r *creativeCanvasRepository) Create(ctx context.Context, u int64, title string) (service.CreativeCanvas, error) {
	b := emptyBoard()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return b, err
	}
	defer tx.Rollback()
	var user int64
	// Serialize the per-owner cap, including two different tabs creating their first board at once.
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, u).Scan(&user); err != nil {
		return b, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM creative_canvases WHERE user_id=$1`, u).Scan(&count); err != nil {
		return b, err
	}
	if count >= service.CanvasMaxBoards {
		return b, service.ErrCanvasLimit
	}
	raw, err := json.Marshal(b.Document)
	if err != nil {
		return b, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO creative_canvases(id,user_id,title,document) VALUES($1,$2,$3,$4::jsonb) RETURNING `+boardFields, uuid.NewString(), u, title, string(raw)).Scan(&b.ID, &b.Title, &b.Revision, &b.UpdatedAt)
	if err != nil {
		return b, err
	}
	return b, tx.Commit()
}
func canvasRead(ctx context.Context, tx *sql.Tx, u int64, id string, lock bool) (service.CreativeCanvas, error) {
	b := emptyBoard()
	var doc []byte
	q := `SELECT ` + boardFields + `,document FROM creative_canvases WHERE user_id=$1 AND id=$2`
	if lock {
		q += ` FOR UPDATE`
	}
	err := tx.QueryRowContext(ctx, q, u, id).Scan(&b.ID, &b.Title, &b.Revision, &b.UpdatedAt, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return b, service.ErrCanvasNotFound
	}
	if err != nil {
		return b, err
	}
	if err = json.Unmarshal(doc, &b.Document); err != nil {
		return b, err
	}
	return b, nil
}
func (r *creativeCanvasRepository) Get(ctx context.Context, u int64, id string) (service.CreativeCanvas, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return service.CreativeCanvas{}, err
	}
	defer tx.Rollback()
	b, err := canvasRead(ctx, tx, u, id, false)
	if err != nil {
		return b, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT node_id::text,request_id::text,created_at FROM creative_canvas_attempts WHERE user_id=$1 AND canvas_id=$2 ORDER BY created_at,request_id`, u, id)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	live := map[string]bool{}
	for _, n := range b.Document.Nodes {
		live[n.ID] = n.Kind == "video-draft"
	}
	for rows.Next() {
		var a service.CanvasAttempt
		if err = rows.Scan(&a.NodeID, &a.RequestID, &a.CreatedAt); err != nil {
			return b, err
		}
		if live[a.NodeID] {
			b.Attempts = append(b.Attempts, a)
		}
	}
	if err = rows.Err(); err != nil {
		return b, err
	}
	return b, tx.Commit()
}

// Old bindings stored {route,batchId,itemId,index} or JSON null. Keep those identities byte-compatible;
// a video binding is identified by its immutable studio job ID, not the URL or extracted bytes.
func canvasInputSnapshot(in service.CanvasInput) ([]byte, error) {
	if in.VideoID != "" {
		return json.Marshal(in)
	}
	return json.Marshal(in.Image)
}

func decodeCanvasInputSnapshot(data []byte) (service.CanvasInput, error) {
	var v struct {
		*service.CanvasImage
		VideoID string `json:"video_id"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &v); err != nil {
			return service.CanvasInput{}, err
		}
	}
	return service.CanvasInput{Image: v.CanvasImage, VideoID: v.VideoID}, nil
}

func (r *creativeCanvasRepository) Save(ctx context.Context, u int64, id string, update service.CanvasUpdate) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := canvasRead(ctx, tx, u, id, true)
	if err != nil {
		return err
	}
	if b.Revision != update.Revision {
		return service.ErrCanvasConflict
	}
	oldLineage, err := b.Document.Lineage()
	if err != nil {
		return err
	}
	lineage, err := update.Document.Lineage()
	if err != nil {
		return err
	}
	if err := service.CheckCanvasGraphVersion(update.GraphVersion, len(oldLineage.Continuation) > 0 || len(lineage.Continuation) > 0); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT request_id::text,node_id::text,source_image FROM creative_canvas_attempts WHERE user_id=$1 AND canvas_id=$2`, u, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	bound := map[string]string{}
	sources := map[string]service.CanvasInput{}
	for rows.Next() {
		var a, n string
		var data []byte
		if err = rows.Scan(&a, &n, &data); err != nil {
			return err
		}
		source, err := decodeCanvasInputSnapshot(data)
		if err != nil {
			return err
		}
		bound[a] = n
		sources[n] = source
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, edge := range lineage.Parent {
		if edge.TakeID != "" && bound[edge.TakeID] != edge.Source {
			return service.ErrCanvasInvalid
		}
	}
	for _, n := range update.Document.Nodes {
		if n.PinnedID != "" && bound[n.PinnedID] != n.ID {
			return service.ErrCanvasInvalid
		}
		if original, ok := sources[n.ID]; ok {
			if n.Kind != "video-draft" {
				return service.ErrCanvasInvalid
			}
			if next := lineage.Input[n.ID]; (next.Image != nil || next.VideoID != "") && !original.Equal(next) {
				return service.ErrCanvasInvalid
			}
		}
	}
	raw, err := json.Marshal(update.Document)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE creative_canvases SET title=$3,document=$4::jsonb,revision=revision+1,updated_at=now() WHERE user_id=$1 AND id=$2`, u, id, update.Title, string(raw))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *creativeCanvasRepository) Remove(ctx context.Context, u int64, id string, rev int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := canvasRead(ctx, tx, u, id, true)
	if err != nil {
		return err
	}
	if b.Revision != rev {
		return service.ErrCanvasConflict
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM creative_canvases WHERE user_id=$1 AND id=$2`, u, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *creativeCanvasRepository) Bind(ctx context.Context, u int64, id, node, request string, revision int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := canvasRead(ctx, tx, u, id, true)
	if err != nil {
		return err
	}
	if b.Revision != revision {
		return service.ErrCanvasConflict
	}
	if !slices.ContainsFunc(b.Document.Nodes, func(n service.CanvasNode) bool { return n.ID == node && n.Kind == "video-draft" }) {
		return service.ErrCanvasNotFound
	}
	lineage, err := b.Document.Lineage()
	if err != nil {
		return err
	}
	source, err := canvasInputSnapshot(lineage.Input[node])
	if err != nil {
		return err
	}
	var board, n string
	var sameInput bool
	err = tx.QueryRowContext(ctx, `SELECT canvas_id::text,node_id::text,source_image IS NOT DISTINCT FROM $3::jsonb FROM creative_canvas_attempts WHERE user_id=$1 AND request_id=$2`, u, request, string(source)).Scan(&board, &n, &sameInput)
	if err == nil {
		if board == id && n == node && sameInput {
			return nil
		}
		return service.ErrCanvasConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count, nodeCount int
	var claimed bool
	var different bool
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE node_id=$3),coalesce(bool_or(node_id=$3 AND source_image IS DISTINCT FROM $4::jsonb),false),EXISTS(SELECT 1 FROM video_studio_jobs WHERE user_id=$1 AND id=$5) FROM creative_canvas_attempts WHERE user_id=$1 AND canvas_id=$2`, u, id, node, string(source), request).Scan(&count, &nodeCount, &different, &claimed)
	if err != nil {
		return err
	}
	if different || claimed {
		return service.ErrCanvasConflict
	}
	if count >= service.CanvasMaxAttempts || nodeCount >= service.CanvasMaxDraftAttempts {
		return service.ErrCanvasLimit
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO creative_canvas_attempts(user_id,canvas_id,node_id,request_id,source_image) VALUES($1,$2,$3,$4,$5::jsonb) ON CONFLICT(user_id,request_id) DO NOTHING`, u, id, node, request, string(source))
	if err != nil {
		return err
	}
	added, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if added != 1 {
		return service.ErrCanvasConflict
	}
	return tx.Commit()
}
func (r *creativeCanvasRepository) ResolveImages(ctx context.Context, u int64, in []service.CanvasImage) (map[service.CanvasImage]bool, error) {
	out := map[service.CanvasImage]bool{}
	if len(in) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT p.route,p."batchId",p."itemId",p.index,i.provider_source_object FROM jsonb_to_recordset($2::jsonb) AS p(route bigint,"batchId" text,"itemId" text,index int)
 JOIN batch_image_jobs j ON j.batch_id=p."batchId" AND j.user_id=$1 AND j.api_key_id=p.route
 JOIN batch_image_items i ON i.job_id=j.batch_id AND i.custom_id=p."itemId"
 WHERE j.user_deleted_at IS NULL AND j.output_deleted_at IS NULL AND (j.output_expires_at IS NULL OR j.output_expires_at>now()) AND j.status='completed' AND i.status='success' AND p.index>=0 AND p.index<i.image_count`, u, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s service.CanvasImage
		var source *string
		if err = rows.Scan(&s.Route, &s.Batch, &s.Item, &s.Index, &source); err != nil {
			return nil, err
		}
		out[s] = !service.IsArchivedBatchImageItem(&service.BatchImageItem{ProviderSourceObject: source})
	}
	return out, rows.Err()
}
func (r *creativeCanvasRepository) ReadJobs(ctx context.Context, u int64, ids []string) ([]service.VideoStudioJob, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+studioColumns+` FROM video_studio_jobs WHERE user_id=$1 AND id=ANY($2::uuid[])`, u, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []service.VideoStudioJob
	for rows.Next() {
		j, e := scanStudioJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
