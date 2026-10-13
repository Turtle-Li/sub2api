package modeltrace

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPythonParity(t *testing.T) {
	data, err := os.ReadFile("testdata/python_parity.json")
	require.NoError(t, err)
	var fixture struct {
		Cases []struct {
			Name     string             `json:"name"`
			Outputs  []OutputItem       `json:"outputs"`
			Expected *AttributionResult `json:"expected"`
		} `json:"cases"`
		Parsers []struct {
			Text    string `json:"text"`
			Numbers []int  `json:"numbers"`
		} `json:"parsers"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	for _, parser := range fixture.Parsers {
		assert.EqualValues(t, parser.Numbers, append([]int{}, ParseNumbers(parser.Text)...))
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := AnalyzeOutputs(tc.Outputs)
			if tc.Expected == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.Expected.Prediction, got.Prediction)
			assert.Equal(t, tc.Expected.FamilyPrediction, got.FamilyPrediction)
			assert.Equal(t, tc.Expected.UsedOutputs, got.UsedOutputs)
			assert.InDelta(t, tc.Expected.Probability, got.Probability, 1e-10)
			assert.InDelta(t, tc.Expected.FamilyProbability, got.FamilyProbability, 1e-10)
			require.Len(t, got.Results, len(tc.Expected.Results))
			for i, expected := range tc.Expected.Results {
				assert.Equal(t, expected.Model, got.Results[i].Model)
				assert.InDelta(t, expected.Score, got.Results[i].Score, 1e-10)
				assert.InDelta(t, expected.Probability, got.Results[i].Probability, 1e-10)
			}
		})
	}
}

func TestChallengeLengthsWithoutReplacement(t *testing.T) {
	challenges := GenerateChallenges(41)
	seen := make(map[int]bool)
	for _, challenge := range challenges {
		require.False(t, seen[challenge.Count])
		seen[challenge.Count] = true
		require.GreaterOrEqual(t, challenge.Count, 292)
		require.LessOrEqual(t, challenge.Count, 332)
		require.Contains(t, challenge.Prompt, strconv.Itoa(challenge.Count))
	}
}

func TestBankLoaded(t *testing.T) {
	bank := GetDefaultBank()
	require.NotNil(t, bank)
	assert.NotEmpty(t, bank.Models)
	assert.NotEmpty(t, bank.Robust.ModelOrder)
	assert.Equal(t, len(bank.Models), len(bank.Robust.ModelOrder))
}

func TestParseNumbers(t *testing.T) {
	text := "Here is the array: [12, 85, 340, 999, 1, 355] and some extra 42"
	nums := ParseNumbers(text)
	// 999 is outside [1, 355], so it should be skipped
	assert.Contains(t, nums, 12)
	assert.Contains(t, nums, 85)
	assert.Contains(t, nums, 340)
	assert.Contains(t, nums, 1)
	assert.Contains(t, nums, 355)
	assert.NotContains(t, nums, 999)
}

func TestExactPythonComparison(t *testing.T) {
	var nums []string
	for i := 0; i < 120; i++ {
		val := (i*37+13)%355 + 1
		nums = append(nums, strconv.Itoa(val))
	}
	text := strings.Join(nums, " ")
	outputs := []OutputItem{
		{Text: text, ExpectedCount: 120},
	}

	res, err := AnalyzeOutputs(outputs)
	require.NoError(t, err)
	require.NotNil(t, res)

	t.Logf("Prediction: %s, Prob: %.6f", res.Prediction, res.Probability)
	for i := 0; i < 5 && i < len(res.Results); i++ {
		t.Logf("  %s: prob=%.6f, score=%.6f", res.Results[i].Model, res.Results[i].Probability, res.Results[i].Score)
	}

	assert.Equal(t, "claude-opus-5-5", res.Prediction)
	assert.InDelta(t, 0.880595, res.Probability, 1e-4)
	assert.InDelta(t, 1.724617, res.Results[0].Score, 1e-4)
}
