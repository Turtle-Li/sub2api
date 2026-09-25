package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 降智测试：管理员用同一道题分别走 BPS（修复）与原生 Codex 路径，人工对比回答质量。
// 结果落库便于异步查看；列表与创建时顺带清理过期记录，不依赖后台任务。

const (
	BPSProbePathBPS    = "bps"
	BPSProbePathNative = "native"

	BPSProbeStatusQueued    = "queued"
	BPSProbeStatusRunning   = "running"
	BPSProbeStatusSucceeded = "succeeded"
	BPSProbeStatusFailed    = "failed"

	bpsProbeMaxAccounts    = 20
	bpsProbeMaxPromptRunes = 8000
	bpsProbeMaxContent     = 1 << 20
	bpsProbeListLimit      = 200
	bpsProbePreviewRunes   = 200
	bpsProbeConcurrency    = 4
	bpsProbeTimeout        = 10 * time.Minute
	bpsProbeRetention      = 7 * 24 * time.Hour
	// 进程重启或切换颜色后遗留的 queued/running 记录超过该时长视为中断。
	bpsProbeStaleAfter = bpsProbeTimeout + 5*time.Minute
)

var bpsProbeEfforts = map[string]struct{}{"": {}, "none": {}, "minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}}

var ErrBPSProbeNotFound = infraerrors.NotFound("BPS_PROBE_NOT_FOUND", "probe result not found")

type BPSProbeResult struct {
	ID              int64      `json:"id"`
	BatchID         string     `json:"batch_id"`
	AccountID       int64      `json:"account_id"`
	AccountName     string     `json:"account_name"`
	Path            string     `json:"path"`
	Model           string     `json:"model"`
	Effort          string     `json:"effort"`
	AppliedEffort   string     `json:"applied_effort"`
	Prompt          string     `json:"prompt"`
	Status          string     `json:"status"`
	Content         string     `json:"content,omitempty"`
	ContentPreview  string     `json:"content_preview"`
	ContentLength   int        `json:"content_length"`
	ErrorMessage    string     `json:"error_message"`
	InputTokens     int        `json:"input_tokens"`
	OutputTokens    int        `json:"output_tokens"`
	ReasoningTokens int        `json:"reasoning_tokens"`
	DurationMs      int64      `json:"duration_ms"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

type BPSProbeRequest struct {
	AccountIDs []int64  `json:"account_ids"`
	Paths      []string `json:"paths"`
	Model      string   `json:"model"`
	Effort     string   `json:"effort"`
	Prompt     string   `json:"prompt"`
}

// bpsProbeOutput 是单次提问的解析结果；出错时仍保留已收到的部分内容。
type bpsProbeOutput struct {
	Content         string
	AppliedEffort   string
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

type bpsProbeRunner interface {
	runOpenAIProbe(ctx context.Context, account *Account, path, model, effort, prompt string) (*bpsProbeOutput, error)
}

type BPSProbeService struct {
	db          *sql.DB
	accountRepo AccountRepository
	runner      bpsProbeRunner
	slots       chan struct{}
}

func NewBPSProbeService(db *sql.DB, accountRepo AccountRepository, tester *AccountTestService) *BPSProbeService {
	return &BPSProbeService{db: db, accountRepo: accountRepo, runner: tester, slots: make(chan struct{}, bpsProbeConcurrency)}
}

func normalizeBPSProbeRequest(req BPSProbeRequest) (BPSProbeRequest, error) {
	req.Model = strings.TrimSpace(req.Model)
	req.Effort = strings.ToLower(strings.TrimSpace(req.Effort))
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Model == "" {
		return req, errors.New("model is required")
	}
	if req.Prompt == "" {
		return req, errors.New("prompt is required")
	}
	if utf8.RuneCountInString(req.Prompt) > bpsProbeMaxPromptRunes {
		return req, fmt.Errorf("prompt too long (max %d characters)", bpsProbeMaxPromptRunes)
	}
	if _, ok := bpsProbeEfforts[req.Effort]; !ok {
		return req, fmt.Errorf("invalid effort: %s", req.Effort)
	}
	req.AccountIDs = normalizeBPSAccountIDs(req.AccountIDs)
	if len(req.AccountIDs) == 0 || len(req.AccountIDs) > bpsProbeMaxAccounts {
		return req, fmt.Errorf("select 1-%d accounts", bpsProbeMaxAccounts)
	}
	paths := make([]string, 0, 2)
	for _, path := range []string{BPSProbePathBPS, BPSProbePathNative} {
		for _, requested := range req.Paths {
			if strings.TrimSpace(requested) == path {
				paths = append(paths, path)
				break
			}
		}
	}
	if len(paths) == 0 {
		return req, errors.New("select at least one path")
	}
	req.Paths = paths
	if containsString(paths, BPSProbePathBPS) && !isBPSUpstreamModel(req.Model) {
		return req, fmt.Errorf("model %s is not served by BPS", req.Model)
	}
	return req, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func bpsProbeAccountSupported(account *Account) bool {
	return account != nil && account.IsOpenAIOAuth() && !account.IsShadow() && !account.IsOpenAIAgentIdentity()
}

func newBPSProbeBatchID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// Create 为每个账号 × 路径建立一条排队记录并异步执行。
func (s *BPSProbeService) Create(ctx context.Context, req BPSProbeRequest) ([]BPSProbeResult, error) {
	req, err := normalizeBPSProbeRequest(req)
	if err != nil {
		return nil, infraerrors.BadRequest("BPS_PROBE_INVALID", err.Error())
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, req.AccountIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	for _, id := range req.AccountIDs {
		account := byID[id]
		if account == nil {
			return nil, infraerrors.BadRequest("BPS_PROBE_INVALID", fmt.Sprintf("account %d not found", id))
		}
		if !bpsProbeAccountSupported(account) {
			return nil, infraerrors.BadRequest("BPS_PROBE_INVALID", fmt.Sprintf("account %d is not a plain OpenAI OAuth account", id))
		}
	}
	s.cleanup(ctx)

	batchID := newBPSProbeBatchID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	created := make([]BPSProbeResult, 0, len(req.AccountIDs)*len(req.Paths))
	for _, id := range req.AccountIDs {
		for _, path := range req.Paths {
			result := BPSProbeResult{BatchID: batchID, AccountID: id, AccountName: byID[id].Name, Path: path, Model: req.Model, Effort: req.Effort, Prompt: req.Prompt, Status: BPSProbeStatusQueued}
			err := tx.QueryRowContext(ctx, `
				INSERT INTO bps_probe_results (batch_id, account_id, account_name, path, model, effort, prompt, status)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				RETURNING id, created_at`,
				result.BatchID, result.AccountID, truncateString(result.AccountName, 200), result.Path, result.Model, result.Effort, result.Prompt, result.Status,
			).Scan(&result.ID, &result.CreatedAt)
			if err != nil {
				return nil, err
			}
			created = append(created, result)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, result := range created {
		go s.run(result)
	}
	return created, nil
}

func (s *BPSProbeService) run(result BPSProbeResult) {
	ctx, cancel := context.WithTimeout(context.Background(), bpsProbeTimeout)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		s.finish(result.ID, time.Time{}, nil, errors.New("timed out waiting for a free probe slot"))
		return
	}

	startedAt := time.Now()
	if _, err := s.db.ExecContext(ctx, `UPDATE bps_probe_results SET status = $2, started_at = $3 WHERE id = $1`, result.ID, BPSProbeStatusRunning, startedAt); err != nil {
		logger.LegacyPrintf("service.bps_probe", "[BPS Probe] mark running failed (id: %d): %v", result.ID, err)
	}
	var output *bpsProbeOutput
	account, err := s.accountRepo.GetByID(ctx, result.AccountID)
	switch {
	case err != nil:
	case !bpsProbeAccountSupported(account):
		err = errors.New("account is no longer a plain OpenAI OAuth account")
	default:
		output, err = s.runner.runOpenAIProbe(ctx, account, result.Path, result.Model, result.Effort, result.Prompt)
	}
	s.finish(result.ID, startedAt, output, err)
}

func (s *BPSProbeService) finish(id int64, startedAt time.Time, output *bpsProbeOutput, runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, errorMessage := BPSProbeStatusSucceeded, ""
	if runErr != nil {
		status, errorMessage = BPSProbeStatusFailed, truncateString(sanitizeUpstreamErrorMessage(runErr.Error()), 2000)
	}
	if output == nil {
		output = &bpsProbeOutput{}
	}
	var durationMs int64
	if !startedAt.IsZero() {
		durationMs = time.Since(startedAt).Milliseconds()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE bps_probe_results
		SET status = $2, content = $3, error_message = $4, applied_effort = $5,
		    input_tokens = $6, output_tokens = $7, reasoning_tokens = $8, duration_ms = $9, finished_at = NOW()
		WHERE id = $1`,
		id, status, strings.ToValidUTF8(output.Content, ""), errorMessage, output.AppliedEffort,
		output.InputTokens, output.OutputTokens, output.ReasoningTokens, durationMs)
	if err != nil {
		logger.LegacyPrintf("service.bps_probe", "[BPS Probe] save result failed (id: %d): %v", id, err)
	}
}

// cleanup 删除过期记录，并把遗留的未完成记录标记为中断。失败只记日志。
func (s *BPSProbeService) cleanup(ctx context.Context) {
	now := time.Now()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM bps_probe_results WHERE created_at < $1`, now.Add(-bpsProbeRetention)); err != nil {
		logger.LegacyPrintf("service.bps_probe", "[BPS Probe] cleanup failed: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE bps_probe_results SET status = $1, error_message = 'interrupted', finished_at = NOW()
		WHERE status IN ($2, $3) AND created_at < $4`,
		BPSProbeStatusFailed, BPSProbeStatusQueued, BPSProbeStatusRunning, now.Add(-bpsProbeStaleAfter)); err != nil {
		logger.LegacyPrintf("service.bps_probe", "[BPS Probe] stale sweep failed: %v", err)
	}
}

const bpsProbeColumns = `id, batch_id, account_id, account_name, path, model, effort, applied_effort, prompt, status,
	error_message, input_tokens, output_tokens, reasoning_tokens, duration_ms, created_at, started_at, finished_at`

type bpsProbeScanner interface {
	Scan(dest ...any) error
}

func scanBPSProbeResult(row bpsProbeScanner, extra ...any) (BPSProbeResult, error) {
	var result BPSProbeResult
	var startedAt, finishedAt sql.NullTime
	dest := []any{&result.ID, &result.BatchID, &result.AccountID, &result.AccountName, &result.Path, &result.Model, &result.Effort,
		&result.AppliedEffort, &result.Prompt, &result.Status, &result.ErrorMessage, &result.InputTokens, &result.OutputTokens,
		&result.ReasoningTokens, &result.DurationMs, &result.CreatedAt, &startedAt, &finishedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return result, err
	}
	if startedAt.Valid {
		result.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		result.FinishedAt = &finishedAt.Time
	}
	return result, nil
}

// List 返回最近的测试记录（不含完整回答，只带预览与长度）。
func (s *BPSProbeService) List(ctx context.Context) ([]BPSProbeResult, error) {
	s.cleanup(ctx)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+bpsProbeColumns+`, LEFT(content, $1), CHAR_LENGTH(content)
		FROM bps_probe_results ORDER BY created_at DESC, id DESC LIMIT $2`, bpsProbePreviewRunes, bpsProbeListLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]BPSProbeResult, 0)
	for rows.Next() {
		var preview string
		var length int
		result, err := scanBPSProbeResult(rows, &preview, &length)
		if err != nil {
			return nil, err
		}
		result.ContentPreview, result.ContentLength = preview, length
		results = append(results, result)
	}
	return results, rows.Err()
}

// Get 返回单条记录的完整回答。
func (s *BPSProbeService) Get(ctx context.Context, id int64) (*BPSProbeResult, error) {
	var content string
	result, err := scanBPSProbeResult(s.db.QueryRowContext(ctx, `SELECT `+bpsProbeColumns+`, content FROM bps_probe_results WHERE id = $1`, id), &content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBPSProbeNotFound
	}
	if err != nil {
		return nil, err
	}
	result.Content, result.ContentLength = content, utf8.RuneCountInString(content)
	return &result, nil
}

func (s *BPSProbeService) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bps_probe_results WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrBPSProbeNotFound
	}
	return nil
}

// DeleteAll 清空全部记录；仍在执行的任务完成时更新不到行，结果随之丢弃。
func (s *BPSProbeService) DeleteAll(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bps_probe_results`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
