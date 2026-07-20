package attachment_gateway

import (
	"bytes"
	"image"
	"image/png"
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
