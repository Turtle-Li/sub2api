package securityaudit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseQwen3GuardStrictAndPolicy(t *testing.T) {
	tests := []struct {
		name, output string
		enabled      []string
		decision     EventDecision
		action       Action
		wantErr      bool
	}{
		{"safe", "Safety: Safe\nCategories: None", AllScannerIDs, EventPass, ActionAllow, false},
		{"controversial", "Safety: Controversial\nCategories: Violent", AllScannerIDs, EventPass, ActionAllow, false},
		{"unsafe", "Safety: Unsafe\nCategories: Jailbreak", AllScannerIDs, EventCritical, ActionBlock, false},
		{"unknown unsafe", "Safety: Unsafe\nCategories: Future Risk", AllScannerIDs, EventCritical, ActionBlock, false},
		{"disabled unsafe warns", "Safety: Unsafe\nCategories: Violent", []string{"PII"}, EventFlag, ActionWarn, false},
		{"extra explanation", "Safety: Safe\nCategories: None\nThis is safe", AllScannerIDs, EventPass, ActionAllow, false},
		{"duplicate", "Safety: Safe\nSafety: Safe", AllScannerIDs, "", "", true},
		{"duplicate empty safety first", "Safety:\nSafety: Safe\nCategories: None", AllScannerIDs, "", "", true},
		{"duplicate empty safety second", "Safety: Safe\nSafety:\nCategories: None", AllScannerIDs, "", "", true},
		{"duplicate categories", "Safety: Safe\nCategories: None\nCategories: PII", AllScannerIDs, "", "", true},
		{"duplicate empty categories first", "Safety: Safe\nCategories:\nCategories: None", AllScannerIDs, "", "", true},
		{"duplicate empty categories second", "Safety: Safe\nCategories: None\nCategories:", AllScannerIDs, "", "", true},
		{"missing categories", "Safety: Safe\n", AllScannerIDs, "", "", true},
		{"unknown safety", "Safety: Maybe\nCategories: PII", AllScannerIDs, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseQwen3Guard(tt.output, tt.enabled)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.decision, result.Decision)
			require.Equal(t, tt.action, result.Action)
		})
	}
}

func TestParseQwen3GuardControversialAlwaysPasses(t *testing.T) {
	tests := []struct {
		name, category string
	}{
		{"jailbreak", "Jailbreak"},
		{"pii", "PII"},
		{"self harm", "Suicide & Self-Harm"},
		{"unknown", "Future Risk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseQwen3Guard("Safety: Controversial\nCategories: "+tt.category, AllScannerIDs)
			require.NoError(t, err)
			require.Equal(t, EventPass, result.Decision)
			require.Equal(t, RiskLow, result.RiskLevel)
			require.Equal(t, ActionAllow, result.Action)
			require.Empty(t, result.Categories)
			require.Empty(t, result.MatchedScanners)
			require.Empty(t, result.UnknownCategories)
			require.Empty(t, result.ScannerScores)
			require.Empty(t, result.ScannerEvidence)
			require.Empty(t, BuildIssueSummaries(*result))
			require.False(t, shouldStorePromptAuditEvent(result.Decision, false))
			require.True(t, shouldStorePromptAuditEvent(result.Decision, true))
			require.Equal(t, 2, result.PolicyVersion)
		})
	}
}

func TestParseQwen3GuardSafeClearsCategoryArtifacts(t *testing.T) {
	for _, category := range []string{"PII", "Future Risk"} {
		t.Run(category, func(t *testing.T) {
			result, err := ParseQwen3Guard("Safety: Safe\nCategories: "+category, AllScannerIDs)
			require.NoError(t, err)
			require.Equal(t, EventPass, result.Decision)
			require.Equal(t, RiskLow, result.RiskLevel)
			require.Equal(t, ActionAllow, result.Action)
			require.Empty(t, result.Categories)
			require.Empty(t, result.MatchedScanners)
			require.Empty(t, result.UnknownCategories)
			require.Empty(t, result.ScannerScores)
			require.Empty(t, result.ScannerEvidence)
			require.Empty(t, BuildIssueSummaries(*result))
		})
	}
}

func TestParseQwen3GuardUnsafeJailbreakBlocksWithFullScore(t *testing.T) {
	result, err := ParseQwen3Guard("Safety: Unsafe\nCategories: Jailbreak", AllScannerIDs)
	require.NoError(t, err)
	require.Equal(t, EventCritical, result.Decision)
	require.Equal(t, RiskCritical, result.RiskLevel)
	require.Equal(t, ActionBlock, result.Action)
	require.Equal(t, 1.0, result.ScannerScores["jailbreak"])
	require.Equal(t, 2, result.PolicyVersion)
}

func TestParseQwen3GuardIgnoresAuxiliaryResponseFields(t *testing.T) {
	result, err := ParseQwen3Guard("Safety: Unsafe\nCategories: Jailbreak\nRefusal: No", AllScannerIDs)
	require.NoError(t, err)
	require.Equal(t, "Unsafe", result.Safety)
	require.Equal(t, []string{"jailbreak"}, result.Categories)

	serialized, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "Refusal")
	require.NotContains(t, string(serialized), "No")
}

func TestQwen3GuardOfficialCategoriesAliasesAndUnknownAreStable(t *testing.T) {
	official := "Violent, Non-violent Illegal Acts, Sexual Content or Sexual Acts, PII, Suicide & Self-Harm, Unethical Acts, Politically Sensitive Topics, Copyright Violation, Jailbreak"
	result, err := ParseQwen3Guard("Safety: Unsafe\nCategories: "+official, AllScannerIDs)
	require.NoError(t, err)
	require.Equal(t, AllScannerIDs, result.MatchedScanners)
	require.Empty(t, result.UnknownCategories)
	require.Equal(t, "priority", result.PolicyID)
	require.Equal(t, 2, result.PolicyVersion)

	aliases := map[string]string{
		"violence": "violent", "non_violent_illegal_acts": "non_violent_illegal_acts",
		"sexual": "sexual_content_or_sexual_acts", "personal identifiable information": "pii",
		"suicide/self harm": "suicide_and_self_harm", "unethical": "unethical_acts",
		"political": "politically_sensitive_topics", "copyright": "copyright_violation",
		"prompt injection": "jailbreak",
	}
	for alias, canonical := range aliases {
		require.Equal(t, canonical, NormalizeCategory(alias), alias)
	}

	const canary = "PROMPT_CANARY_RAW_UNKNOWN_CATEGORY"
	unknown, err := ParseQwen3Guard("Safety: Unsafe\nCategories: "+canary, AllScannerIDs)
	require.NoError(t, err)
	require.Len(t, unknown.UnknownCategories, 1)
	require.NotContains(t, unknown.UnknownCategories[0], "canary")
	require.NotContains(t, unknown.UnknownCategories[0], "raw")
	require.Contains(t, unknown.UnknownCategories[0], "unknown:")
}

func TestExtractOpenAIContentSupportsStringAndTextBlocks(t *testing.T) {
	content, err := extractOpenAIContent([]byte(`{"choices":[{"message":{"content":"Safety: Safe\nCategories: None"}}]}`))
	require.NoError(t, err)
	require.Equal(t, "Safety: Safe\nCategories: None", content)
	content, err = extractOpenAIContent([]byte(`{"choices":[{"message":{"content":[{"type":"text","text":"Safety: Safe"},{"type":"text","text":"Categories: None"}]}}]}`))
	require.NoError(t, err)
	require.Equal(t, "Safety: Safe\nCategories: None", content)
	for _, body := range []string{`{}`, `{"choices":[]}`, `{"choices":[{"message":{"content":null}}]}`} {
		_, err := extractOpenAIContent([]byte(body))
		require.Error(t, err)
	}
}

func TestAggregateRequiresEveryResult(t *testing.T) {
	_, err := AggregateResults([]*NormalizedResult{{Decision: EventPass, Action: ActionAllow}, nil}, 0)
	require.Error(t, err)
	result, err := AggregateResults([]*NormalizedResult{
		{Decision: EventPass, RiskLevel: RiskLow, Action: ActionAllow, Categories: []string{"pii"}},
		{Decision: EventCritical, RiskLevel: RiskCritical, Action: ActionBlock, Categories: []string{"jailbreak"}},
	}, 0)
	require.NoError(t, err)
	require.Equal(t, EventCritical, result.Decision)
	require.Equal(t, ActionBlock, result.Action)
	require.Equal(t, []string{"pii", "jailbreak"}, result.Categories)
}

func TestAggregateNonUnsafeDoesNotContaminateUnsafeFacts(t *testing.T) {
	safe, err := ParseQwen3Guard("Safety: Safe\nCategories: Future Risk", AllScannerIDs)
	require.NoError(t, err)
	controversial, err := ParseQwen3Guard("Safety: Controversial\nCategories: Jailbreak, Future Risk", AllScannerIDs)
	require.NoError(t, err)
	unsafe, err := ParseQwen3Guard("Safety: Unsafe\nCategories: PII", AllScannerIDs)
	require.NoError(t, err)

	result, err := AggregateResults([]*NormalizedResult{safe, controversial, unsafe}, 0)
	require.NoError(t, err)
	require.Equal(t, EventCritical, result.Decision)
	require.Equal(t, []string{"pii"}, result.Categories)
	require.Equal(t, []string{"pii"}, result.MatchedScanners)
	require.Empty(t, result.UnknownCategories)
	require.Equal(t, map[string]float64{"pii": 1}, result.ScannerScores)
	require.Equal(t, map[string]string{"pii": ScannerCatalog["pii"].Label}, result.ScannerEvidence)
}

func TestAggregateDeduplicatesFactsAndUsesMostSevereEndpointMetadata(t *testing.T) {
	result, err := AggregateResults([]*NormalizedResult{
		{Decision: EventPass, RiskLevel: RiskLow, Action: ActionAllow, Safety: "Safe", Categories: []string{"pii"}, MatchedScanners: []string{"pii"}, ScannerScores: map[string]float64{"pii": 0}, ScannerEvidence: map[string]string{"pii": "first"}, GuardEndpointID: "safe-node", ScannerVersion: "safe-version", PolicyID: "priority", PolicyVersion: 1},
		{Decision: EventCritical, RiskLevel: RiskCritical, Action: ActionBlock, Safety: "Unsafe", Categories: []string{"pii", "jailbreak"}, MatchedScanners: []string{"pii", "jailbreak"}, ScannerScores: map[string]float64{"pii": 1, "jailbreak": 1}, ScannerEvidence: map[string]string{"pii": "second", "jailbreak": "blocked"}, GuardEndpointID: "block-node", ScannerVersion: "block-version", PolicyID: "priority", PolicyVersion: 2},
	}, 7*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, []string{"pii", "jailbreak"}, result.Categories)
	require.Equal(t, []string{"pii", "jailbreak"}, result.MatchedScanners)
	require.Equal(t, "first", result.ScannerEvidence["pii"], "evidence is deterministically first-seen")
	require.Equal(t, "block-node", result.GuardEndpointID)
	require.Equal(t, "block-version", result.ScannerVersion)
	require.Equal(t, 2, result.PolicyVersion)
	require.Equal(t, 7, result.LatencyMS)
}

func TestIssueSummariesAreDeterministicRedactedDerivedDTOs(t *testing.T) {
	const canary = "PROMPT_CANARY_EVIDENCE_SECRET"
	result := NormalizedResult{
		Decision: EventCritical, RiskLevel: RiskCritical, Action: ActionBlock,
		Categories: []string{"jailbreak", "pii"}, MatchedScanners: []string{"pii"},
		ScannerScores: map[string]float64{"pii": 1}, ScannerEvidence: map[string]string{"pii": canary},
		UnknownCategories: []string{unknownCategoryID("future risk")},
	}
	summaries := BuildIssueSummaries(result)
	require.Len(t, summaries, 3, "known categories are not hidden merely because policy disabled one")
	raw, err := json.Marshal(summaries)
	require.NoError(t, err)
	require.NotContains(t, string(raw), canary)
	for _, summary := range summaries {
		require.NotEmpty(t, summary.Title)
		require.NotEmpty(t, summary.Description)
		require.NotEmpty(t, summary.Code)
		require.NotEmpty(t, summary.EvidenceHash)
	}
}
