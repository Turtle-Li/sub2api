package attachment_gateway

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"

	webp "github.com/gen2brain/webp"
	"github.com/stretchr/testify/require"
)

// BenchmarkWebPEncoderMethods measures the libwebp quality/speed trade-off on
// production-sized opaque, text/UI and transparent fixtures. Run it with
// -benchtime=1x so each sub-benchmark reports one cold encode.
func BenchmarkWebPEncoderMethods(b *testing.B) {
	photoBytes := makePhotoLikePNG(b, 1920, 1280, 20260720)
	photo, err := png.Decode(bytes.NewReader(photoBytes))
	require.NoError(b, err)
	transparentBytes := makeTransparentPNG(b, 1920, 1280)
	transparent, err := png.Decode(bytes.NewReader(transparentBytes))
	require.NoError(b, err)

	cases := []struct {
		name     string
		image    image.Image
		quality  int
		lossless bool
	}{
		{name: "photo_q85", image: photo, quality: 85},
		{name: "text_ui_q90", image: makeCodeLikeImage(1920, 1280), quality: 90},
		{name: "transparent_lossless", image: transparent, quality: 100, lossless: true},
	}

	for _, testCase := range cases {
		for _, method := range []int{0, 2, 4, 6} {
			b.Run(testCase.name+"/method_"+string(rune('0'+method)), func(b *testing.B) {
				var output bytes.Buffer
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					output.Reset()
					err := webp.Encode(&output, testCase.image, webp.Options{
						Quality:  testCase.quality,
						Lossless: testCase.lossless,
						Method:   method,
						Exact:    true,
					})
					require.NoError(b, err)
				}
				b.StopTimer()
				b.ReportMetric(float64(output.Len()), "encoded_B")
			})
		}
	}
}

// BenchmarkTransparentWebPStrategies compares the conservative Phase 1
// lossless policy with high-quality lossy WebP. WebP stores alpha separately,
// so lossy RGB does not imply lossy transparency. Composite error on both a
// black and white background is a closer proxy for what a user sees than raw
// RGB error in fully transparent pixels.
//
// Run with -benchtime=1x. The benchmark intentionally contains no latency or
// quality assertions; it is an investigation fixture whose metrics can guide
// policy changes without making normal test runs machine-speed dependent.
func BenchmarkTransparentWebPStrategies(b *testing.B) {
	patternPNG := makeTransparentPNG(b, 1920, 1280)
	pattern, err := png.Decode(bytes.NewReader(patternPNG))
	require.NoError(b, err)
	complex := makeComplexTransparentImage(1920, 1280)
	complexPNG := encodePNGForBenchmark(b, complex)
	hiddenRGB := makeHiddenRGBTransparentImage(1920, 1280)
	hiddenRGBPNG := encodePNGForBenchmark(b, hiddenRGB)

	fixtures := []struct {
		name     string
		image    image.Image
		pngBytes []byte
	}{
		{name: "pattern_partial_alpha", image: pattern, pngBytes: patternPNG},
		{name: "complex_partial_alpha", image: complex, pngBytes: complexPNG},
		{name: "hidden_rgb", image: hiddenRGB, pngBytes: hiddenRGBPNG},
	}
	strategies := []struct {
		name     string
		quality  int
		lossless bool
		exact    bool
	}{
		{name: "lossless_exact", quality: 100, lossless: true, exact: true},
		{name: "lossless_nonexact", quality: 100, lossless: true, exact: false},
		{name: "lossy_q90", quality: 90, exact: true},
		{name: "lossy_q95", quality: 95, exact: true},
	}

	for _, fixture := range fixtures {
		for _, strategy := range strategies {
			b.Run(fixture.name+"/"+strategy.name, func(b *testing.B) {
				var output bytes.Buffer
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					output.Reset()
					err := webp.Encode(&output, fixture.image, webp.Options{
						Quality:  strategy.quality,
						Lossless: strategy.lossless,
						Method:   webpEncoderMethod,
						Exact:    strategy.exact,
					})
					require.NoError(b, err)
				}
				b.StopTimer()

				decodedAll, err := webp.DecodeAll(bytes.NewReader(output.Bytes()))
				require.NoError(b, err)
				require.NotEmpty(b, decodedAll.Image)
				decoded := decodedAll.Image[0]
				b.ReportMetric(float64(len(fixture.pngBytes)), "source_png_B")
				b.ReportMetric(float64(output.Len()), "encoded_B")
				b.ReportMetric(
					(1-float64(output.Len())/float64(len(fixture.pngBytes)))*100,
					"savings_pct",
				)
				b.ReportMetric(meanAbsoluteAlphaError(fixture.image, decoded), "alpha_MAE")
				b.ReportMetric(compositedRGBRMSE(fixture.image, decoded, 0), "black_bg_RMSE")
				b.ReportMetric(compositedRGBRMSE(fixture.image, decoded, 255), "white_bg_RMSE")
			})
		}
	}
}

func encodePNGForBenchmark(b testing.TB, source image.Image) []byte {
	b.Helper()
	var output bytes.Buffer
	require.NoError(b, png.Encode(&output, source))
	return output.Bytes()
}

// makeComplexTransparentImage has high-entropy color and a mix of fully
// transparent, partially transparent and opaque pixels. It exercises the
// expensive/low-savings corner that a blanket lossless policy handles poorly.
func makeComplexTransparentImage(width, height int) *image.NRGBA {
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	state := uint32(0x5eed2026)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			state = state*1664525 + 1013904223
			alphaSelector := byte(state >> 24)
			alpha := uint8(255)
			switch {
			case alphaSelector < 32:
				alpha = 0
			case alphaSelector < 160:
				alpha = uint8(32 + int(alphaSelector)*222/159)
			}
			output.SetNRGBA(x, y, color.NRGBA{
				R: uint8(state),
				G: uint8(state>>8) ^ uint8(x*13+y*3),
				B: uint8(state>>16) ^ uint8(x*5+y*11),
				A: alpha,
			})
		}
	}
	return output
}

// makeHiddenRGBTransparentImage mimics exported design assets that retain
// arbitrary RGB values under fully transparent pixels. Exact=false may discard
// those invisible values while preserving the rendered result.
func makeHiddenRGBTransparentImage(width, height int) *image.NRGBA {
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	state := uint32(0xc0ffee42)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			state = state*1103515245 + 12345
			alpha := uint8(0)
			if x > width/5 && x < width*4/5 && y > height/4 && y < height*3/4 {
				alpha = 255
			}
			output.SetNRGBA(x, y, color.NRGBA{
				R: uint8(state),
				G: uint8(state >> 8),
				B: uint8(state >> 16),
				A: alpha,
			})
		}
	}
	return output
}

func meanAbsoluteAlphaError(left, right image.Image) float64 {
	bounds := left.Bounds()
	var total uint64
	var samples uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			leftPixel := toNRGBA(left.At(x, y))
			rightPixel := toNRGBA(right.At(x, y))
			total += uint64(absDiff8(leftPixel.A, rightPixel.A))
			samples++
		}
	}
	return float64(total) / float64(samples)
}

func compositedRGBRMSE(left, right image.Image, background uint8) float64 {
	bounds := left.Bounds()
	var squaredTotal float64
	var samples float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			leftPixel := toNRGBA(left.At(x, y))
			rightPixel := toNRGBA(right.At(x, y))
			for _, pair := range [][2]uint8{
				{leftPixel.R, rightPixel.R},
				{leftPixel.G, rightPixel.G},
				{leftPixel.B, rightPixel.B},
			} {
				leftComposite := compositeChannel(pair[0], leftPixel.A, background)
				rightComposite := compositeChannel(pair[1], rightPixel.A, background)
				delta := float64(int(leftComposite) - int(rightComposite))
				squaredTotal += delta * delta
				samples++
			}
		}
	}
	return math.Sqrt(squaredTotal / samples)
}

func toNRGBA(value color.Color) color.NRGBA {
	converted, ok := color.NRGBAModel.Convert(value).(color.NRGBA)
	if !ok {
		panic("color.NRGBAModel returned a non-NRGBA value")
	}
	return converted
}

func compositeChannel(value, alpha, background uint8) uint8 {
	return uint8((int(value)*int(alpha) + int(background)*(255-int(alpha)) + 127) / 255)
}

func absDiff8(left, right uint8) int {
	if left >= right {
		return int(left - right)
	}
	return int(right - left)
}
