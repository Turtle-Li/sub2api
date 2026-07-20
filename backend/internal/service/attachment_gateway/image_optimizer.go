package attachment_gateway

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	webp "github.com/gen2brain/webp"
)

const (
	// Attachment optimization sits directly on the request path. Method 0
	// keeps libwebp's quality setting while favoring bounded encode latency;
	// production-sized fixtures retain the required savings with substantially
	// less CPU and allocation than the exhaustive method 6 search.
	webpEncoderMethod = 0
	webpEncoderID     = "gen2brain-webp-v0.6.4-method0"
)

type libwebpEncoder struct{}

func (libwebpEncoder) ID() string { return webpEncoderID }

func (libwebpEncoder) Encode(source image.Image, options encodeOptions) ([]byte, error) {
	var output bytes.Buffer
	err := webp.Encode(&output, source, webp.Options{
		Quality:  options.Quality,
		Lossless: options.Lossless,
		Method:   webpEncoderMethod,
		Exact:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("attachment gateway: encode WebP: %w", err)
	}
	return output.Bytes(), nil
}

func decodeImage(source []byte, mimeType string, maxPixels int64) (image.Image, int, int, error) {
	if mimeType == "image/webp" && isAnimatedWebP(source) {
		return nil, 0, 0, errAnimatedImage
	}
	if mimeType == "image/png" && isAnimatedPNG(source) {
		return nil, 0, 0, errAnimatedImage
	}

	reader := bytes.NewReader(source)
	var (
		config image.Config
		err    error
	)
	switch mimeType {
	case "image/png":
		config, err = png.DecodeConfig(reader)
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(reader)
	case "image/webp":
		config, err = webp.DecodeConfig(reader)
	default:
		return nil, 0, 0, errUnsupportedMediaType
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("attachment gateway: decode image config: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > maxPixels/int64(config.Height) {
		return nil, 0, 0, errTooManyPixels
	}

	reader.Reset(source)
	var decoded image.Image
	switch mimeType {
	case "image/png":
		decoded, err = png.Decode(reader)
	case "image/jpeg":
		decoded, err = jpeg.Decode(reader)
	case "image/webp":
		decoded, err = webp.Decode(reader)
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("attachment gateway: decode image: %w", err)
	}
	return decoded, config.Width, config.Height, nil
}

func chooseImagePolicy(source image.Image, cfg Config) imagePolicy {
	analysis := analyzeRaster(source)
	if analysis.HasTransparency {
		return imagePolicy{Quality: 100, Lossless: true, Reason: "transparency"}
	}
	if analysis.LikelyTextOrUI {
		return imagePolicy{Quality: cfg.SpecialQuality, Reason: "text_or_ui"}
	}
	return imagePolicy{Quality: cfg.Quality, Reason: "default"}
}

type rasterAnalysis struct {
	HasTransparency bool
	LikelyTextOrUI  bool
}

// analyzeRaster uses a bounded sample grid. It does not try to infer semantic
// content; it only recognizes the high-edge/limited-palette pattern common to
// source code and UI screenshots so Phase 1 can choose the conservative q90
// policy without resizing.
func analyzeRaster(source image.Image) rasterAnalysis {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return rasterAnalysis{}
	}
	step := max(1, int(math.Ceil(float64(max(width, height))/512.0)))
	palette := make(map[uint16]struct{}, 1024)
	var edges, comparisons int
	hasTransparency := false

	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		var previousLuma uint32
		hasPrevious := false
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r, g, b, a := source.At(x, y).RGBA()
			if a < 0xffff {
				hasTransparency = true
			}
			if len(palette) <= 1024 {
				key := uint16((r>>12)<<8 | (g>>12)<<4 | (b >> 12))
				palette[key] = struct{}{}
			}
			luma := (299*r + 587*g + 114*b) / 1000
			if hasPrevious {
				comparisons++
				delta := int64(luma) - int64(previousLuma)
				if delta < 0 {
					delta = -delta
				}
				if delta >= 28*257 {
					edges++
				}
			}
			previousLuma = luma
			hasPrevious = true
		}
	}
	edgeRatio := 0.0
	if comparisons > 0 {
		edgeRatio = float64(edges) / float64(comparisons)
	}
	limitedPalette := len(palette) <= 512
	return rasterAnalysis{
		HasTransparency: hasTransparency,
		LikelyTextOrUI:  limitedPalette && edgeRatio >= 0.02,
	}
}

func isAnimatedWebP(source []byte) bool {
	if len(source) < 12 || string(source[:4]) != "RIFF" || string(source[8:12]) != "WEBP" {
		return false
	}
	for offset := 12; offset+8 <= len(source); {
		chunkType := string(source[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(source[offset+4 : offset+8]))
		if chunkType == "ANIM" || chunkType == "ANMF" {
			return true
		}
		if chunkSize < 0 || offset > len(source)-8-chunkSize {
			return false
		}
		offset += 8 + chunkSize + chunkSize%2
	}
	return false
}

func isAnimatedPNG(source []byte) bool {
	if len(source) < 8 || !bytes.Equal(source[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return false
	}
	for offset := 8; offset+12 <= len(source); {
		chunkSize := int(binary.BigEndian.Uint32(source[offset : offset+4]))
		chunkType := string(source[offset+4 : offset+8])
		if strings.EqualFold(chunkType, "acTL") {
			return true
		}
		if chunkSize < 0 || offset > len(source)-12-chunkSize {
			return false
		}
		offset += 12 + chunkSize
	}
	return false
}
