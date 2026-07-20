package attachment_gateway

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	webp "github.com/gen2brain/webp"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestQualityCodeScreenshotOCR(t *testing.T) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("tesseract is not installed; skipping local OCR quality proxy")
	}
	source := makeReadableCodeScreenshot()
	baseline := runTesseract(t, source)
	require.NotEmpty(t, baseline)
	encoder := libwebpEncoder{}

	for _, quality := range []int{85, 90} {
		encoded, err := encoder.Encode(source, encodeOptions{Quality: quality})
		require.NoError(t, err)
		decoded, err := webp.Decode(bytes.NewReader(encoded))
		require.NoError(t, err)
		candidate := runTesseract(t, decoded)
		similarity := textSimilarity(baseline, candidate)
		t.Logf("code OCR q%d: bytes=%d similarity=%.4f", quality, len(encoded), similarity)
		require.GreaterOrEqual(t, similarity, 0.95)
	}
}

func TestQualityUIEdgesAndPhotoPSNR(t *testing.T) {
	encoder := libwebpEncoder{}
	ui := makeReadableCodeScreenshot()
	photoBytes := makePhotoLikePNG(t, 900, 650, 77)
	photo, err := png.Decode(bytes.NewReader(photoBytes))
	require.NoError(t, err)

	for _, quality := range []int{85, 90} {
		uiEncoded, err := encoder.Encode(ui, encodeOptions{Quality: quality})
		require.NoError(t, err)
		uiDecoded, err := webp.Decode(bytes.NewReader(uiEncoded))
		require.NoError(t, err)
		edgeF1 := edgeF1Score(ui, uiDecoded)
		require.GreaterOrEqual(t, edgeF1, 0.90)

		photoEncoded, err := encoder.Encode(photo, encodeOptions{Quality: quality})
		require.NoError(t, err)
		photoDecoded, err := webp.Decode(bytes.NewReader(photoEncoded))
		require.NoError(t, err)
		psnr := rgbPSNR(photo, photoDecoded)
		t.Logf(
			"quality q%d: ui_bytes=%d ui_edge_f1=%.4f photo_bytes=%d photo_psnr_db=%.2f",
			quality,
			len(uiEncoded),
			edgeF1,
			len(photoEncoded),
			psnr,
		)
		require.GreaterOrEqual(t, psnr, 25.0)
	}
}

func TestQualityUIDashboardSmallTextAndStructure(t *testing.T) {
	ui := makeReadableUIDashboard()
	encoder := libwebpEncoder{}
	tesseractAvailable := true
	if _, err := exec.LookPath("tesseract"); err != nil {
		tesseractAvailable = false
	}
	baselineOCR := ""
	if tesseractAvailable {
		baselineOCR = runTesseract(t, ui)
		require.NotEmpty(t, baselineOCR)
	}

	for _, quality := range []int{85, 90} {
		encoded, err := encoder.Encode(ui, encodeOptions{Quality: quality})
		require.NoError(t, err)
		decoded, err := webp.Decode(bytes.NewReader(encoded))
		require.NoError(t, err)
		edgeF1 := edgeF1Score(ui, decoded)
		require.GreaterOrEqual(t, edgeF1, 0.90)

		ocrSimilarity := -1.0
		if tesseractAvailable {
			ocrSimilarity = textSimilarity(baselineOCR, runTesseract(t, decoded))
			require.GreaterOrEqual(t, ocrSimilarity, 0.95)
		}
		t.Logf(
			"UI dashboard q%d: bytes=%d edge_f1=%.4f ocr_similarity=%.4f",
			quality,
			len(encoded),
			edgeF1,
			ocrSimilarity,
		)
	}
}

func makeReadableCodeScreenshot() image.Image {
	small := image.NewNRGBA(image.Rect(0, 0, 600, 300))
	for index := range small.Pix {
		small.Pix[index] = 255
	}
	for y := 0; y < small.Bounds().Dy(); y++ {
		for x := 0; x < small.Bounds().Dx(); x++ {
			small.SetNRGBA(x, y, color.NRGBA{R: 16, G: 22, B: 31, A: 255})
		}
	}
	drawer := &font.Drawer{
		Dst:  small,
		Src:  image.NewUniform(color.NRGBA{R: 230, G: 238, B: 246, A: 255}),
		Face: basicfont.Face7x13,
	}
	lines := []string{
		"func optimizeAttachment(body []byte) error {",
		"  hash := sha256.Sum256(body)",
		"  cached, ok := imageCache[hash]",
		"  if ok { return cached }",
		"  return encodeWebP(body, 85)",
		"}",
		"expected request body: 14 MB -> 2 MB",
	}
	for index, line := range lines {
		drawer.Dot = fixed.P(18, 35+index*34)
		drawer.DrawString(line)
	}
	large := image.NewNRGBA(image.Rect(0, 0, 1800, 900))
	draw.NearestNeighbor.Scale(large, large.Bounds(), small, small.Bounds(), draw.Src, nil)
	return large
}

func makeReadableUIDashboard() image.Image {
	small := image.NewNRGBA(image.Rect(0, 0, 700, 420))
	fillNRGBARect(small, small.Bounds(), color.NRGBA{R: 244, G: 247, B: 251, A: 255})
	fillNRGBARect(small, image.Rect(0, 0, 700, 54), color.NRGBA{R: 25, G: 35, B: 55, A: 255})
	fillNRGBARect(small, image.Rect(22, 78, 216, 174), color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	fillNRGBARect(small, image.Rect(238, 78, 432, 174), color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	fillNRGBARect(small, image.Rect(454, 78, 678, 174), color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	fillNRGBARect(small, image.Rect(22, 196, 678, 392), color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	lightText := image.NewUniform(color.NRGBA{R: 237, G: 244, B: 252, A: 255})
	darkText := image.NewUniform(color.NRGBA{R: 31, G: 45, B: 67, A: 255})
	mutedText := image.NewUniform(color.NRGBA{R: 78, G: 96, B: 120, A: 255})
	drawer := &font.Drawer{Dst: small, Face: basicfont.Face7x13}
	drawer.Src = lightText
	drawer.Dot = fixed.P(22, 34)
	drawer.DrawString("Attachment Gateway Dashboard")

	drawer.Src = mutedText
	for _, label := range []struct {
		x, y int
		text string
	}{
		{36, 104, "REQUEST BODY"},
		{252, 104, "CACHE HIT"},
		{468, 104, "OPTIMIZE LATENCY"},
		{36, 224, "REQUEST VOLUME"},
	} {
		drawer.Dot = fixed.P(label.x, label.y)
		drawer.DrawString(label.text)
	}
	drawer.Src = darkText
	for _, value := range []struct {
		x, y int
		text string
	}{
		{36, 145, "14 MB -> 2 MB"},
		{252, 145, "87 percent"},
		{468, 145, "184 ms warm"},
		{36, 372, "HTTP stable   WS ctx_pool stable"},
	} {
		drawer.Dot = fixed.P(value.x, value.y)
		drawer.DrawString(value.text)
	}

	barHeights := []int{42, 78, 58, 104, 92, 132, 116, 150, 138, 164}
	for index, height := range barHeights {
		x := 54 + index*54
		fillNRGBARect(
			small,
			image.Rect(x, 350-height, x+26, 350),
			color.NRGBA{R: 43, G: 119, B: 229, A: 255},
		)
	}

	large := image.NewNRGBA(image.Rect(0, 0, 1400, 840))
	draw.NearestNeighbor.Scale(large, large.Bounds(), small, small.Bounds(), draw.Src, nil)
	return large
}

func fillNRGBARect(destination *image.NRGBA, bounds image.Rectangle, value color.NRGBA) {
	bounds = bounds.Intersect(destination.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			destination.SetNRGBA(x, y, value)
		}
	}
}

func runTesseract(t *testing.T, source image.Image) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ocr.png")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(file, source))
	require.NoError(t, file.Close())
	command := exec.Command("tesseract", path, "stdout", "--psm", "6", "-l", "eng")
	output, err := command.Output()
	require.NoError(t, err)
	return strings.Join(strings.Fields(strings.ToLower(string(output))), " ")
}

func textSimilarity(left, right string) float64 {
	if left == right {
		return 1
	}
	if left == "" || right == "" {
		return 0
	}
	distance := levenshteinDistance([]rune(left), []rune(right))
	return 1 - float64(distance)/float64(max(len([]rune(left)), len([]rune(right))))
}

func levenshteinDistance(left, right []rune) int {
	previous := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range left {
		current := make([]int, len(right)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range right {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[rightIndex+1] = min(
				current[rightIndex]+1,
				previous[rightIndex+1]+1,
				previous[rightIndex]+cost,
			)
		}
		previous = current
	}
	return previous[len(right)]
}

func edgeF1Score(left, right image.Image) float64 {
	bounds := left.Bounds()
	var truePositive, falsePositive, falseNegative int
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X + 2; x < bounds.Max.X; x += 2 {
			leftEdge := lumaDifference(left, x-2, y, x, y) >= 32
			rightEdge := lumaDifference(right, x-2, y, x, y) >= 32
			switch {
			case leftEdge && rightEdge:
				truePositive++
			case !leftEdge && rightEdge:
				falsePositive++
			case leftEdge && !rightEdge:
				falseNegative++
			}
		}
	}
	denominator := 2*truePositive + falsePositive + falseNegative
	if denominator == 0 {
		return 1
	}
	return float64(2*truePositive) / float64(denominator)
}

func lumaDifference(source image.Image, x1, y1, x2, y2 int) int {
	left := pixelLuma(source.At(x1, y1))
	right := pixelLuma(source.At(x2, y2))
	return int(math.Abs(float64(left - right)))
}

func pixelLuma(value color.Color) int {
	r, g, b, _ := value.RGBA()
	return int((299*r + 587*g + 114*b) / 1000 / 257)
}

func rgbPSNR(left, right image.Image) float64 {
	bounds := left.Bounds()
	var squaredError float64
	var samples float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 2 {
			lr, lg, lb, _ := left.At(x, y).RGBA()
			rr, rg, rb, _ := right.At(x, y).RGBA()
			for _, delta := range []float64{
				float64(int64(lr)-int64(rr)) / 257,
				float64(int64(lg)-int64(rg)) / 257,
				float64(int64(lb)-int64(rb)) / 257,
			} {
				squaredError += delta * delta
				samples++
			}
		}
	}
	if squaredError == 0 {
		return math.Inf(1)
	}
	meanSquaredError := squaredError / samples
	return 10 * math.Log10(255*255/meanSquaredError)
}
