package modeltrace

import (
	cryptoRand "crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"unicode"
)

//go:embed unified_bank.json
var defaultBankJSON []byte

const (
	ValueMin           = 1
	ValueMax           = 355
	Dimension          = ValueMax - ValueMin + 1
	Alpha              = 0.5
	OrderedBlockWeight = 0.25
)

// Bank represents the ModelTrace statistical fingerprint bank.
type Bank struct {
	Schema      string                      `json:"schema"`
	Models      []ModelEntry                `json:"models"`
	Robust      RobustConfig                `json:"robust"`
	Calibration map[string]CalibrationEntry `json:"calibration"`
}

type ModelEntry struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	Family           string `json:"family"`
	FamilyName       string `json:"family_name"`
	ResponseCount    int    `json:"response_count"`
	ValidNumberCount int    `json:"valid_number_count"`
	Counts           []int  `json:"counts"`
}

type RobustConfig struct {
	ModelOrder    []string            `json:"model_order"`
	Hellinger     HellingerConfig     `json:"hellinger"`
	OrderedBlocks OrderedBlocksConfig `json:"ordered_blocks"`
}

type HellingerConfig struct {
	FeatureMean   []float64   `json:"feature_mean"`
	FeatureScale  []float64   `json:"feature_scale"`
	NuisanceBasis [][]float64 `json:"nuisance_basis"`
	Centroids     [][]float64 `json:"centroids"`
}

type OrderedBlocksConfig struct {
	Weight               float64       `json:"weight"`
	FeatureMean          []float64     `json:"feature_mean"`
	FeatureScale         []float64     `json:"feature_scale"`
	EnvironmentCentroids [][][]float64 `json:"environment_centroids"`
	NuisanceBasis        [][]float64   `json:"nuisance_basis"`
	Centroids            [][]float64   `json:"centroids"`
}

type CalibrationEntry struct {
	Beta       float64 `json:"beta"`
	CVAccuracy float64 `json:"cv_accuracy"`
}

var defaultBank *Bank

func init() {
	var b Bank
	if err := json.Unmarshal(defaultBankJSON, &b); err != nil {
		panic(fmt.Sprintf("modeltrace: failed to parse embedded unified_bank.json: %v", err))
	}
	defaultBank = &b
}

// GetDefaultBank returns the embedded default bank.
func GetDefaultBank() *Bank {
	return defaultBank
}

// GetActiveBank returns the default bank.
func GetActiveBank() *Bank {
	return defaultBank
}

// OutputItem represents one probed model completion.
type OutputItem struct {
	Text          string `json:"text"`
	ExpectedCount int    `json:"expected_count"`
}

// ModelResult represents attribution score for one candidate model.
type ModelResult struct {
	Model       string  `json:"model"`
	DisplayName string  `json:"display_name"`
	Probability float64 `json:"probability"`
	Family      string  `json:"family"`
	FamilyName  string  `json:"family_name"`
	Score       float64 `json:"score"`
}

// AttributionResult represents the full attribution output.
type AttributionResult struct {
	Prediction           string        `json:"prediction"`
	PredictionName       string        `json:"prediction_name"`
	Probability          float64       `json:"probability"`
	FamilyPrediction     string        `json:"family_prediction"`
	FamilyPredictionName string        `json:"family_prediction_name"`
	FamilyProbability    float64       `json:"family_probability"`
	UsedOutputs          int           `json:"used_outputs"`
	Results              []ModelResult `json:"results"`
}

var digitRegex = regexp.MustCompile(`\p{Nd}+`)

// Python's \d and int() accept Unicode decimal digits as well as ASCII.
func parseDecimal(text string) int {
	value := 0
	for _, r := range text {
		digit := -1
		for _, span := range unicode.Digit.R16 {
			if r >= rune(span.Lo) && r <= rune(span.Hi) && (r-rune(span.Lo))%rune(span.Stride) == 0 {
				digit = int((r-rune(span.Lo))/rune(span.Stride)) % 10
				break
			}
		}
		if digit < 0 {
			for _, span := range unicode.Digit.R32 {
				if r >= rune(span.Lo) && r <= rune(span.Hi) && (r-rune(span.Lo))%rune(span.Stride) == 0 {
					digit = int((r-rune(span.Lo))/rune(span.Stride)) % 10
					break
				}
			}
		}
		if digit < 0 || value > ValueMax {
			return ValueMax + 1
		}
		value = value*10 + digit
	}
	return value
}

// ParseNumbers extracts the longest sequence of numbers between 1 and 355
// where runs are separated by alphabetic letters.
func ParseNumbers(text string) []int {
	type run []int
	var runs []run
	var current run
	previousEnd := 0

	matches := digitRegex.FindAllStringIndex(text, -1)
	for _, m := range matches {
		start, end := m[0], m[1]
		separator := text[previousEnd:start]
		val := parseDecimal(text[start:end])
		if len(current) > 0 {
			hasLetter := false
			for _, r := range separator {
				if unicode.IsLetter(r) {
					hasLetter = true
					break
				}
			}
			if hasLetter {
				runs = append(runs, current)
				current = run{}
			}
		}
		if val >= ValueMin && val <= ValueMax {
			current = append(current, val)
		}
		previousEnd = end
	}
	if len(current) > 0 {
		runs = append(runs, current)
	}

	bestLen := 0
	var best run
	for _, r := range runs {
		if len(r) > bestLen {
			bestLen = len(r)
			best = r
		}
	}
	return best
}

// CountNumbers counts frequencies of numbers [1, 355].
func CountNumbers(numbers []int) []int {
	counts := make([]int, Dimension)
	for _, n := range numbers {
		if n >= ValueMin && n <= ValueMax {
			counts[n-ValueMin]++
		}
	}
	return counts
}

func standardize(values []float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	varSum := 0.0
	for _, v := range values {
		diff := v - mean
		varSum += diff * diff
	}
	variance := varSum / float64(len(values))
	scale := math.Sqrt(variance)
	if scale < 1e-12 {
		scale = 1e-12
	}

	res := make([]float64, len(values))
	for i, v := range values {
		res[i] = (v - mean) / scale
	}
	return res
}

func hellingerFeature(counts []int) []float64 {
	total := 0.0
	values := make([]float64, len(counts))
	for i, c := range counts {
		v := float64(c) + Alpha
		values[i] = v
		total += v
	}
	feat := make([]float64, len(counts))
	for i, v := range values {
		feat[i] = math.Sqrt(v / total)
	}
	return feat
}

func dot(a, b []float64) float64 {
	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func norm(a []float64) float64 {
	return math.Sqrt(dot(a, a))
}

func subtractBasis(projected []float64, basis [][]float64) []float64 {
	if len(basis) == 0 {
		return projected
	}
	// In numpy: (projected @ basis.T) @ basis
	// First compute all coefficients against original projected vector:
	coeffs := make([]float64, len(basis))
	for i, b := range basis {
		coeffs[i] = dot(projected, b)
	}
	res := make([]float64, len(projected))
	copy(res, projected)
	for i, b := range basis {
		for j := range res {
			res[j] -= coeffs[i] * b[j]
		}
	}
	return res
}

func normalized(a []float64) []float64 {
	n := norm(a)
	if n < 1e-12 {
		n = 1e-12
	}
	res := make([]float64, len(a))
	for i, v := range a {
		res[i] = v / n
	}
	return res
}

func splitIntoFour(numbers []int) [][]int {
	base := len(numbers) / 4
	rem := len(numbers) % 4
	chunks := make([][]int, 4)
	start := 0
	for i := 0; i < 4; i++ {
		size := base
		if i < rem {
			size++
		}
		chunks[i] = numbers[start : start+size]
		start += size
	}
	return chunks
}

func orderedBlockFeature(numbers []int) []float64 {
	var pieces []float64
	for _, chunk := range splitIntoFour(numbers) {
		bins := make([]float64, 16)
		for i := range bins {
			bins[i] = 0.5
		}
		for _, val := range chunk {
			idx := int(((float64(val) - 1.0) / 355.0) * 16.0)
			if idx > 15 {
				idx = 15
			}
			if idx < 0 {
				idx = 0
			}
			bins[idx] += 1.0
		}
		total := 0.0
		for _, b := range bins {
			total += b
		}
		for _, b := range bins {
			pieces = append(pieces, math.Sqrt(b/total))
		}
	}

	lastDigits := make([]float64, 10)
	for i := range lastDigits {
		lastDigits[i] = 0.5
	}
	for _, val := range numbers {
		mod := val % 10
		if mod >= 0 && mod < 10 {
			lastDigits[mod] += 1.0
		}
	}
	lastTotal := 0.0
	for _, d := range lastDigits {
		lastTotal += d
	}
	for _, d := range lastDigits {
		pieces = append(pieces, math.Sqrt(d/lastTotal))
	}
	return pieces
}

func robustScoreCounts(counts []int, bank *Bank) []float64 {
	art := bank.Robust.Hellinger
	feat := hellingerFeature(counts)
	projected := make([]float64, len(feat))
	for i, v := range feat {
		projected[i] = (v - art.FeatureMean[i]) / art.FeatureScale[i]
	}
	projected = subtractBasis(projected, art.NuisanceBasis)
	projected = normalized(projected)

	scores := make([]float64, len(art.Centroids))
	for i, centroid := range art.Centroids {
		scores[i] = dot(projected, centroid)
	}
	return standardize(standardize(scores))
}

func orderedBlockScores(numbers []int, bank *Bank) []float64 {
	art := bank.Robust.OrderedBlocks
	feat := orderedBlockFeature(numbers)
	standardizedFeature := make([]float64, len(feat))
	for i, v := range feat {
		standardizedFeature[i] = (v - art.FeatureMean[i]) / art.FeatureScale[i]
	}
	unit := normalized(standardizedFeature)

	envScores := make([][]float64, len(art.EnvironmentCentroids))
	for envIdx, envCentroids := range art.EnvironmentCentroids {
		row := make([]float64, len(envCentroids))
		for mIdx, centroid := range envCentroids {
			row[mIdx] = dot(unit, centroid)
		}
		envScores[envIdx] = row
	}

	modelCount := len(art.Centroids)
	templateRaw := make([]float64, modelCount)
	for mIdx := 0; mIdx < modelCount; mIdx++ {
		maxScore := -math.MaxFloat64
		for envIdx := 0; envIdx < len(envScores); envIdx++ {
			if envScores[envIdx][mIdx] > maxScore {
				maxScore = envScores[envIdx][mIdx]
			}
		}
		templateRaw[mIdx] = maxScore
	}
	template := standardize(templateRaw)

	projected := normalized(subtractBasis(standardizedFeature, art.NuisanceBasis))
	nuisanceRaw := make([]float64, modelCount)
	for i, centroid := range art.Centroids {
		nuisanceRaw[i] = dot(projected, centroid)
	}
	nuisance := standardize(nuisanceRaw)

	combined := make([]float64, modelCount)
	for i := 0; i < modelCount; i++ {
		combined[i] = 0.5*template[i] + 0.5*nuisance[i]
	}
	return standardize(combined)
}

func robustScoreNumbers(numbers []int, bank *Bank) []float64 {
	marginal := robustScoreCounts(CountNumbers(numbers), bank)
	art := bank.Robust.OrderedBlocks
	weight := art.Weight
	if weight <= 0 {
		return marginal
	}
	ordered := orderedBlockScores(numbers, bank)
	res := make([]float64, len(marginal))
	for i := range marginal {
		res[i] = (1.0-weight)*marginal[i] + weight*ordered[i]
	}
	return res
}

func softmax(values []float64) []float64 {
	maxVal := -math.MaxFloat64
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	weights := make([]float64, len(values))
	total := 0.0
	for i, v := range values {
		w := math.Exp(v - maxVal)
		weights[i] = w
		total += w
	}
	res := make([]float64, len(values))
	for i, w := range weights {
		res[i] = w / total
	}
	return res
}

// AnalyzeOutputs computes model attribution using the currently active bank.
func AnalyzeOutputs(outputs []OutputItem) (*AttributionResult, error) {
	return AnalyzeOutputsWithBank(outputs, GetActiveBank())
}

// AnalyzeOutputsWithBank computes model attribution using a custom bank.
func AnalyzeOutputsWithBank(outputs []OutputItem, bank *Bank) (*AttributionResult, error) {
	if bank == nil {
		return nil, fmt.Errorf("modeltrace: fingerprint bank is nil")
	}
	type validItem struct {
		numbers []int
		scores  []float64
	}
	var valid []validItem
	for _, item := range outputs {
		expected := item.ExpectedCount
		minNums := 80
		if expected > 0 {
			calc := int(math.Ceil(float64(expected) * 0.55))
			if calc > minNums {
				minNums = calc
			}
		}
		nums := ParseNumbers(item.Text)
		if len(nums) >= minNums {
			scores := robustScoreNumbers(nums, bank)
			valid = append(valid, validItem{numbers: nums, scores: scores})
		}
	}

	if len(valid) == 0 {
		return nil, fmt.Errorf("modeltrace: no valid completion outputs met number threshold")
	}

	modelIds := make([]string, len(bank.Models))
	for i, m := range bank.Models {
		modelIds[i] = m.ID
	}
	modelCount := len(modelIds)

	combinedScores := make([]float64, modelCount)
	for mIdx := 0; mIdx < modelCount; mIdx++ {
		sum := 0.0
		for _, v := range valid {
			sum += v.scores[mIdx]
		}
		combinedScores[mIdx] = sum / float64(len(valid))
	}

	calKey := strconv.Itoa(min(len(valid), 3))
	beta := 12.0
	if cal, ok := bank.Calibration[calKey]; ok && cal.Beta > 0 {
		beta = cal.Beta
	}

	scaledScores := make([]float64, modelCount)
	for i, s := range combinedScores {
		scaledScores[i] = beta * s
	}
	probabilities := softmax(scaledScores)

	modelEntries := make(map[string]ModelEntry, len(bank.Models))
	var familyOrder []string
	familySeen := make(map[string]bool)
	for _, m := range bank.Models {
		modelEntries[m.ID] = m
		fam := m.Family
		if fam == "" {
			fam = "models"
		}
		if !familySeen[fam] {
			familySeen[fam] = true
			familyOrder = append(familyOrder, fam)
		}
	}

	familyNames := make(map[string]string)
	for _, fam := range familyOrder {
		name := fam
		for _, m := range bank.Models {
			if (m.Family == fam || (m.Family == "" && fam == "models")) && m.FamilyName != "" {
				name = m.FamilyName
				break
			}
		}
		familyNames[fam] = name
	}

	results := make([]ModelResult, modelCount)
	for i, mID := range modelIds {
		mEntry := modelEntries[mID]
		fam := mEntry.Family
		if fam == "" {
			fam = "models"
		}
		results[i] = ModelResult{
			Model:       mID,
			DisplayName: mEntry.DisplayName,
			Probability: probabilities[i],
			Family:      fam,
			FamilyName:  familyNames[fam],
			Score:       combinedScores[i],
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Probability > results[j].Probability
	})

	familyProbs := make(map[string]float64)
	for _, r := range results {
		familyProbs[r.Family] += r.Probability
	}

	winningFam := ""
	winningFamProb := -1.0
	for _, fam := range familyOrder {
		p := familyProbs[fam]
		if p > winningFamProb {
			winningFamProb = p
			winningFam = fam
		}
	}

	return &AttributionResult{
		Prediction:           results[0].Model,
		PredictionName:       results[0].DisplayName,
		Probability:          results[0].Probability,
		FamilyPrediction:     winningFam,
		FamilyPredictionName: familyNames[winningFam],
		FamilyProbability:    winningFamProb,
		UsedOutputs:          len(valid),
		Results:              results,
	}, nil
}

// Challenge represents a probe prompt.
type Challenge struct {
	ID     string `json:"id"`
	Count  int    `json:"count"`
	Prompt string `json:"prompt"`
}

var (
	challengeOpenings = []string{
		"这是一次独立的数值选择记录",
		"请完成下面的无语义整数选择任务",
		"执行一次第一反应取值记录",
		"生成一组不承载语义的整数选择",
		"进行一轮快速逐项取值",
	}
	challengeActions = []string{
		"为各个位置分别凭第一反应选择",
		"逐项选择",
		"每次只决定当前一项，共给出",
		"分别凭第一反应给出",
		"逐个直接选择",
	}
	challengeEndings = []string{
		"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。",
		"偶然重复是有效的；不要重新排列或修正已经写出的项目。",
		"相同值可以再次出现；输出过程中不要整理或改写前面的项目。",
		"重复值无需删除；不要筛选、重排或补成某种规律。",
		"不必赋予数字任何含义；已经给出的值保持不变。",
	}
	challengeSeparators = []string{
		"数字之间用逗号或空格分隔均可。",
		"使用一种一致的常见分隔符即可。",
		"可以用逗号、空格或换行分隔。",
		"只要每个整数边界清楚，格式可自行选择。",
	}
)

func cryptoRandChoice(list []string) string {
	if len(list) == 0 {
		return ""
	}
	n, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(int64(len(list))))
	if err != nil {
		return list[0]
	}
	return list[n.Int64()]
}

func cryptoRandIntRange(minVal, maxVal int) int {
	diff := maxVal - minVal + 1
	if diff <= 0 {
		return minVal
	}
	n, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(int64(diff)))
	if err != nil {
		return minVal
	}
	return minVal + int(n.Int64())
}

// GenerateChallenges generates challenge prompts faithfully matching ModelTrace.
func GenerateChallenges(count int) []Challenge {
	if count <= 0 {
		count = 3
	}
	challenges := make([]Challenge, count)
	lengths := make([]int, 41)
	for i := range lengths {
		lengths[i] = 292 + i
	}
	for i := 0; i < count; i++ {
		// Sample without replacement, matching random.sample(range(292, 333), count).
		// Larger batches start a new permutation after exhausting the range.
		position := i % len(lengths)
		selected := cryptoRandIntRange(position, len(lengths)-1)
		lengths[position], lengths[selected] = lengths[selected], lengths[position]
		length := lengths[position]
		opening := cryptoRandChoice(challengeOpenings)
		action := cryptoRandChoice(challengeActions)
		ending := cryptoRandChoice(challengeEndings)
		separator := cryptoRandChoice(challengeSeparators)

		prompt := fmt.Sprintf(
			"%s。%s %d 个 1 到 355（含端点）的整数。"+
				"每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。"+
				"本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、"+
				"计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。"+
				"%s%s"+
				"直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。",
			opening, action, length, ending, separator,
		)

		randomBytes := make([]byte, 7)
		_, _ = cryptoRand.Read(randomBytes)
		challengeID := fmt.Sprintf("probe-%d-%s", i+1, hex.EncodeToString(randomBytes))

		challenges[i] = Challenge{
			ID:     challengeID,
			Count:  length,
			Prompt: prompt,
		}
	}
	return challenges
}
