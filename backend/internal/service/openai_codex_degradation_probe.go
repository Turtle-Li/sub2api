package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 降智测试：管理员对已固定 turn-state 票据的账号发一道题，人工判断回答质量。
// 请求与网关一致地注入固定票据与路由 Cookie，只测"修复后"的路径；不经调度、不计费、不写使用记录。
// 结果落库便于异步查看；列表与创建时顺带清理过期记录，不依赖后台任务。
//
// 表名沿用 bps_probe_results（迁移 259）：蓝绿切换期间旧实例仍读写该表，改名会让旧实例报错。
// 新记录 path 固定为 turn_state；旧的 bps/native 记录不再展示，只随清空或 7 天保留期删除。

const (
	CodexDegradationProbePathTurnState = "turn_state"

	CodexDegradationProbeStatusQueued    = "queued"
	CodexDegradationProbeStatusRunning   = "running"
	CodexDegradationProbeStatusSucceeded = "succeeded"
	CodexDegradationProbeStatusFailed    = "failed"

	codexDegradationProbeMaxAccounts    = 20
	codexDegradationProbeMaxPromptRunes = 8000
	codexDegradationProbeMaxContent     = 1 << 20
	codexDegradationProbeListLimit      = 200
	codexDegradationProbePreviewRunes   = 200
	codexDegradationProbeConcurrency    = 4
	codexDegradationProbeTimeout        = 10 * time.Minute
	codexDegradationProbeRetention      = 7 * 24 * time.Hour
	codexDegradationProbeErrorBodyLimit = 4096
	// 进程重启或切换颜色后遗留的 queued/running 记录超过该时长视为中断。
	codexDegradationProbeStaleAfter = codexDegradationProbeTimeout + 5*time.Minute
)

var codexDegradationProbeEfforts = map[string]struct{}{"": {}, "none": {}, "minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}}

var (
	ErrCodexDegradationProbeNotFound = infraerrors.NotFound("CODEX_DEGRADATION_PROBE_NOT_FOUND", "probe result not found")
	// errCodexDegradationProbeNoPinnedState 运行时票据已失效（创建后过期或被删除）。
	errCodexDegradationProbeNoPinnedState = errors.New("票据已过期或不存在")
)

type CodexDegradationProbeResult struct {
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

type CodexDegradationProbeRequest struct {
	AccountIDs []int64 `json:"account_ids"`
	Model      string  `json:"model"`
	Effort     string  `json:"effort"`
	Prompt     string  `json:"prompt"`
}

// codexDegradationProbeOutput 是单次提问的解析结果；出错时仍保留已收到的部分内容。
type codexDegradationProbeOutput struct {
	Content         string
	AppliedEffort   string
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

type codexDegradationProbeRunner interface {
	runCodexDegradationProbe(ctx context.Context, account *Account, model, effort, prompt string) (*codexDegradationProbeOutput, error)
}

type CodexDegradationProbeService struct {
	db          *sql.DB
	accountRepo AccountRepository
	runner      codexDegradationProbeRunner
	slots       chan struct{}
}

func NewCodexDegradationProbeService(db *sql.DB, accountRepo AccountRepository, tester *AccountTestService) *CodexDegradationProbeService {
	return &CodexDegradationProbeService{db: db, accountRepo: accountRepo, runner: tester, slots: make(chan struct{}, codexDegradationProbeConcurrency)}
}

func normalizeCodexDegradationProbeRequest(req CodexDegradationProbeRequest) (CodexDegradationProbeRequest, error) {
	req.Model = strings.TrimSpace(req.Model)
	req.Effort = strings.ToLower(strings.TrimSpace(req.Effort))
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Model == "" {
		return req, errors.New("model is required")
	}
	if req.Prompt == "" {
		return req, errors.New("prompt is required")
	}
	if utf8.RuneCountInString(req.Prompt) > codexDegradationProbeMaxPromptRunes {
		return req, fmt.Errorf("prompt too long (max %d characters)", codexDegradationProbeMaxPromptRunes)
	}
	if _, ok := codexDegradationProbeEfforts[req.Effort]; !ok {
		return req, fmt.Errorf("invalid effort: %s", req.Effort)
	}
	ids := make([]int64, 0, len(req.AccountIDs))
	for _, id := range req.AccountIDs {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	req.AccountIDs = slices.Compact(ids)
	if len(req.AccountIDs) == 0 || len(req.AccountIDs) > codexDegradationProbeMaxAccounts {
		return req, fmt.Errorf("select 1-%d accounts", codexDegradationProbeMaxAccounts)
	}
	return req, nil
}

func codexDegradationProbeAccountSupported(account *Account) bool {
	return account != nil && account.IsOpenAIOAuth() && !account.IsShadow() && !account.IsOpenAIAgentIdentity()
}

// codexDegradationProbeUpstreamModel 与网关一致地解析上游模型，固定票据按该模型匹配。
func codexDegradationProbeUpstreamModel(account *Account, model string) string {
	return normalizeOpenAIModelForUpstream(account, account.GetMappedModel(model))
}

func newCodexDegradationProbeBatchID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// Create 为每个账号建立一条排队记录并异步执行。账号必须对该模型持有有效固定票据。
func (s *CodexDegradationProbeService) Create(ctx context.Context, req CodexDegradationProbeRequest) ([]CodexDegradationProbeResult, error) {
	req, err := normalizeCodexDegradationProbeRequest(req)
	if err != nil {
		return nil, infraerrors.BadRequest("CODEX_DEGRADATION_PROBE_INVALID", err.Error())
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
			return nil, infraerrors.BadRequest("CODEX_DEGRADATION_PROBE_INVALID", fmt.Sprintf("account %d not found", id))
		}
		if !codexDegradationProbeAccountSupported(account) {
			return nil, infraerrors.BadRequest("CODEX_DEGRADATION_PROBE_INVALID", fmt.Sprintf("account %d is not a plain OpenAI OAuth account", id))
		}
		upstreamModel := codexDegradationProbeUpstreamModel(account, req.Model)
		if account.GetPinnedCodexTurnState(upstreamModel) == "" {
			return nil, infraerrors.BadRequest("CODEX_DEGRADATION_PROBE_NO_PINNED_STATE",
				fmt.Sprintf("account %d has no active pinned turn-state for model %s", id, upstreamModel))
		}
	}
	s.cleanup(ctx)

	batchID := newCodexDegradationProbeBatchID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	created := make([]CodexDegradationProbeResult, 0, len(req.AccountIDs))
	for _, id := range req.AccountIDs {
		result := CodexDegradationProbeResult{BatchID: batchID, AccountID: id, AccountName: byID[id].Name, Path: CodexDegradationProbePathTurnState, Model: req.Model, Effort: req.Effort, Prompt: req.Prompt, Status: CodexDegradationProbeStatusQueued}
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
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, result := range created {
		go s.run(result)
	}
	return created, nil
}

func (s *CodexDegradationProbeService) run(result CodexDegradationProbeResult) {
	ctx, cancel := context.WithTimeout(context.Background(), codexDegradationProbeTimeout)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		s.finish(result.ID, time.Time{}, nil, errors.New("timed out waiting for a free probe slot"))
		return
	}

	startedAt := time.Now()
	if _, err := s.db.ExecContext(ctx, `UPDATE bps_probe_results SET status = $2, started_at = $3 WHERE id = $1`, result.ID, CodexDegradationProbeStatusRunning, startedAt); err != nil {
		logger.LegacyPrintf("service.codex_degradation_probe", "[Codex Degradation Probe] mark running failed (id: %d): %v", result.ID, err)
	}
	var output *codexDegradationProbeOutput
	account, err := s.accountRepo.GetByID(ctx, result.AccountID)
	switch {
	case err != nil:
	case !codexDegradationProbeAccountSupported(account):
		err = errors.New("account is no longer a plain OpenAI OAuth account")
	default:
		output, err = s.runner.runCodexDegradationProbe(ctx, account, result.Model, result.Effort, result.Prompt)
	}
	s.finish(result.ID, startedAt, output, err)
}

func (s *CodexDegradationProbeService) finish(id int64, startedAt time.Time, output *codexDegradationProbeOutput, runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, errorMessage := CodexDegradationProbeStatusSucceeded, ""
	if runErr != nil {
		status, errorMessage = CodexDegradationProbeStatusFailed, truncateString(sanitizeUpstreamErrorMessage(runErr.Error()), 2000)
	}
	if output == nil {
		output = &codexDegradationProbeOutput{}
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
		logger.LegacyPrintf("service.codex_degradation_probe", "[Codex Degradation Probe] save result failed (id: %d): %v", id, err)
	}
}

// cleanup 删除过期记录，并把遗留的未完成记录标记为中断。失败只记日志。
func (s *CodexDegradationProbeService) cleanup(ctx context.Context) {
	now := time.Now()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM bps_probe_results WHERE created_at < $1`, now.Add(-codexDegradationProbeRetention)); err != nil {
		logger.LegacyPrintf("service.codex_degradation_probe", "[Codex Degradation Probe] cleanup failed: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE bps_probe_results SET status = $1, error_message = 'interrupted', finished_at = NOW()
		WHERE status IN ($2, $3) AND created_at < $4`,
		CodexDegradationProbeStatusFailed, CodexDegradationProbeStatusQueued, CodexDegradationProbeStatusRunning, now.Add(-codexDegradationProbeStaleAfter)); err != nil {
		logger.LegacyPrintf("service.codex_degradation_probe", "[Codex Degradation Probe] stale sweep failed: %v", err)
	}
}

const codexDegradationProbeColumns = `id, batch_id, account_id, account_name, path, model, effort, applied_effort, prompt, status,
	error_message, input_tokens, output_tokens, reasoning_tokens, duration_ms, created_at, started_at, finished_at`

type codexDegradationProbeScanner interface {
	Scan(dest ...any) error
}

func scanCodexDegradationProbeResult(row codexDegradationProbeScanner, extra ...any) (CodexDegradationProbeResult, error) {
	var result CodexDegradationProbeResult
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
func (s *CodexDegradationProbeService) List(ctx context.Context) ([]CodexDegradationProbeResult, error) {
	s.cleanup(ctx)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+codexDegradationProbeColumns+`, LEFT(content, $1), CHAR_LENGTH(content)
		FROM bps_probe_results WHERE path = $2 ORDER BY created_at DESC, id DESC LIMIT $3`,
		codexDegradationProbePreviewRunes, CodexDegradationProbePathTurnState, codexDegradationProbeListLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]CodexDegradationProbeResult, 0)
	for rows.Next() {
		var preview string
		var length int
		result, err := scanCodexDegradationProbeResult(rows, &preview, &length)
		if err != nil {
			return nil, err
		}
		result.ContentPreview, result.ContentLength = preview, length
		results = append(results, result)
	}
	return results, rows.Err()
}

// Get 返回单条记录的完整回答。
func (s *CodexDegradationProbeService) Get(ctx context.Context, id int64) (*CodexDegradationProbeResult, error) {
	var content string
	result, err := scanCodexDegradationProbeResult(s.db.QueryRowContext(ctx,
		`SELECT `+codexDegradationProbeColumns+`, content FROM bps_probe_results WHERE id = $1 AND path = $2`,
		id, CodexDegradationProbePathTurnState), &content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCodexDegradationProbeNotFound
	}
	if err != nil {
		return nil, err
	}
	result.Content, result.ContentLength = content, utf8.RuneCountInString(content)
	return &result, nil
}

func (s *CodexDegradationProbeService) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bps_probe_results WHERE id = $1 AND path = $2`, id, CodexDegradationProbePathTurnState)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrCodexDegradationProbeNotFound
	}
	return nil
}

// DeleteAll 清空全部记录（含旧版 bps/native 记录）；仍在执行的任务完成时更新不到行，结果随之丢弃。
func (s *CodexDegradationProbeService) DeleteAll(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bps_probe_results`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
