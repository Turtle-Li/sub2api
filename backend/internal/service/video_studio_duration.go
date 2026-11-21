package service

import (
	"encoding/binary"
	"math"
)

// Only complete boxes inside the already-read prefix are examined. No codec, external resource,
// seek to an untrusted offset, or allocation derived from a box length is involved.
func studioBox(b []byte) (kind string, payload, rest []byte, ok bool) {
	if len(b) < 8 {
		return
	}
	size, h := uint64(binary.BigEndian.Uint32(b[:4])), 8
	kind = string(b[4:8])
	if size == 1 {
		if len(b) < 16 {
			return
		}
		size = binary.BigEndian.Uint64(b[8:16])
		h = 16
	}
	if size < uint64(h) || size > uint64(len(b)) {
		return
	}
	return kind, b[h:size], b[size:], true
}

// A non-fragmented movie's mvhd contains its declared presentation duration, including edit lists.
// Fragmented files may reserve a provisional duration; never infer their total from a prefix.
func studioMovieHeaderDuration(prefix []byte) int64 {
	var movie []byte
	for len(prefix) > 0 {
		kind, data, rest, ok := studioBox(prefix)
		if !ok {
			return 0
		}
		if kind == "moov" {
			movie = data
			break
		}
		prefix = rest
	}
	var header []byte
	for len(movie) > 0 {
		kind, data, rest, ok := studioBox(movie)
		if !ok || kind == "mvex" {
			return 0
		}
		if kind == "mvhd" {
			if header != nil {
				return 0
			}
			header = data
		}
		movie = rest
	}
	if len(header) < 20 {
		return 0
	}
	var ticks uint64
	var scale uint32
	switch header[0] {
	case 0:
		if len(header) < 100 {
			return 0
		}
		scale = binary.BigEndian.Uint32(header[12:16])
		v := binary.BigEndian.Uint32(header[16:20])
		if v == math.MaxUint32 {
			return 0
		}
		ticks = uint64(v)
	case 1:
		if len(header) < 112 {
			return 0
		}
		scale = binary.BigEndian.Uint32(header[20:24])
		ticks = binary.BigEndian.Uint64(header[24:32])
		if ticks == math.MaxUint64 {
			return 0
		}
	default:
		return 0
	}
	if scale == 0 || ticks == 0 {
		return 0
	}
	s := uint64(scale)
	whole, rem := ticks/s, ticks%s
	const max = 1<<53 - 1
	if whole > max/1000 {
		return 0
	}
	ms := whole*1000 + (rem*1000+s/2)/s
	if ms == 0 || ms > max {
		return 0
	}
	return int64(ms)
}
