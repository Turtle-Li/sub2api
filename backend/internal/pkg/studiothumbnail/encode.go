package studiothumbnail

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaxPixels = 40_000_000
	edge      = 480
)

func Encode(r io.ReadSeeker) ([]byte, error) {
	config, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > MaxPixels/int64(config.Height) {
		return nil, errors.New("image thumbnail source exceeds pixel limit")
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	source, _, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	w, h := config.Width, config.Height
	if longest := max(w, h); longest > edge {
		w, h = max(1, w*edge/longest), max(1, h*edge/longest)
	}
	output := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(output, output.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(output, output.Bounds(), source, source.Bounds(), draw.Over, nil)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, output, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
