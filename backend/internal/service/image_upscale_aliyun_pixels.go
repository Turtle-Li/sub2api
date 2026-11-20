package service

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

const aliyunImageMaxUpload = 5 << 20

type aliyunInput struct {
	data                      []byte
	original, content, canvas image.Point
}

// The service accepts 32x32 minimum, 1920 long / 1080 short maximum. Only
// adapt a working copy; the delivery geometry always derives from the original.
func prepareAliyunInput(source []byte) (*aliyunInput, error) {
	w, h, _, err := validateUpscaleSource(source)
	if err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, nil)
	}
	original := image.Pt(w, h)
	fit := min(1., 1920./float64(max(w, h)), 1080./float64(min(w, h)))
	for {
		content := image.Pt(max(1, int(math.Round(float64(w)*fit))), max(1, int(math.Round(float64(h)*fit))))
		canvas := image.Pt(max(32, content.X), max(32, content.Y))
		in := &aliyunInput{original: original, content: content, canvas: canvas}
		if canvas == original && content == original && len(source) <= aliyunImageMaxUpload {
			in.data = source
			return in, nil
		}
		dst := image.NewNRGBA(image.Rectangle{Max: canvas})
		draw.ApproxBiLinear.Scale(dst, image.Rectangle{Max: content}, src, src.Bounds(), draw.Src, nil)
		var b bytes.Buffer
		// Prefer lossless, including alpha. Reduce the working copy only when the
		// encoded image would exceed the provider cap (a noisy RGBA source can).
		if err := png.Encode(&b, dst); err != nil {
			return nil, imageUpscaleError("ALIYUN_SOURCE_ENCODE", 0, false, nil)
		}
		if b.Len() <= aliyunImageMaxUpload {
			in.data = b.Bytes()
			return in, nil
		}
		fit *= 0.85
	}
}

func (in *aliyunInput) result(data []byte, scale int, limit int64) (*ImageUpscaleResult, error) {
	w, h, mime, err := decodeUpscaleImageConfig(data)
	if err != nil || (mime != "image/png" && mime != "image/jpeg") {
		return nil, imageUpscaleError("ALIYUN_RESULT_IMAGE", 0, false, nil)
	}
	if w != in.canvas.X*scale || h != in.canvas.Y*scale {
		return nil, imageUpscaleError("ALIYUN_RESULT_DIMENSIONS", 0, false, nil)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, imageUpscaleError("ALIYUN_RESULT_IMAGE", 0, false, nil)
	}
	target := in.original.Mul(scale)
	if in.original != in.canvas || in.content != in.canvas {
		dst := image.NewNRGBA(image.Rectangle{Max: target})
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, image.Rectangle{Max: in.content.Mul(scale)}, draw.Src, nil)
		var b bytes.Buffer
		if mime == "image/jpeg" {
			err = jpeg.Encode(&b, dst, &jpeg.Options{Quality: 95})
		} else {
			err = png.Encode(&b, dst)
		}
		if err != nil || int64(b.Len()) > limit {
			return nil, imageUpscaleError("ALIYUN_RESULT_ENCODE", 0, false, nil)
		}
		data = b.Bytes()
	}
	return &ImageUpscaleResult{Data: data, MimeType: mime, Width: target.X, Height: target.Y, Scale: scale}, nil
}
