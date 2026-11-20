package service

import (
	"encoding/binary"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestStudioMovieHeaderDuration(t *testing.T) {
	box := func(k string, p []byte) []byte {
		b := make([]byte, 8+len(p))
		binary.BigEndian.PutUint32(b, uint32(len(b)))
		copy(b[4:8], k)
		copy(b[8:], p)
		return b
	}
	mvhd := func(v byte, s uint32, d uint64) []byte {
		b := make([]byte, 100)
		if v == 1 {
			b = make([]byte, 112)
			binary.BigEndian.PutUint32(b[20:24], s)
			binary.BigEndian.PutUint64(b[24:32], d)
		} else {
			binary.BigEndian.PutUint32(b[12:16], s)
			binary.BigEndian.PutUint32(b[16:20], uint32(d))
		}
		b[0] = v
		return box("mvhd", b)
	}
	good := mvhd(0, 48000, 480000)
	other := box("free", []byte{4, 5})
	for _, tc := range []struct {
		name string
		in   []byte
		want int64
	}{
		{"file header not requested seconds", append(box("ftyp", nil), box("moov", good)...), 10000},
		{"v1 and fractional rounding", box("moov", append(other, mvhd(1, 90000, 930150)...)), 10335},
		{"zero timebase", box("moov", mvhd(0, 0, 10)), 0}, {"unknown v0", box("moov", mvhd(0, 1, math.MaxUint32)), 0},
		{"unknown v1", box("moov", mvhd(1, 1, math.MaxUint64)), 0}, {"unsafe magnitude", box("moov", mvhd(1, 1, math.MaxUint64-1)), 0},
		{"fragmented header", box("moov", append(append([]byte{}, good...), box("mvex", nil)...)), 0},
		{"incomplete movie box", box("moov", good)[:25], 0}, {"short version", box("moov", box("mvhd", make([]byte, 20))), 0},
		{"duplicate", box("moov", append(append([]byte{}, good...), good...)), 0}, {"metadata in payload is not movie", box("mdat", box("moov", good)), 0}, {"large invalid length", []byte{0, 0, 0, 1, 'm', 'o', 'o', 'v', 255, 255, 255, 255, 255, 255, 255, 255}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, studioMovieHeaderDuration(tc.in)) })
	}
}
