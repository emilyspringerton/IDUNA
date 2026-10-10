// Package hslz4 decodes the LZ4 block format (the wire format of liblz4's LZ4_compress_default) and
// the small frame wrapper the HRIP/WOTAN uplink uses: a body is a sequence of
//
//	[u32 LE rawLen][u32 LE compLen][compLen bytes of one LZ4 block that decodes to exactly rawLen]
//
// with rawLen <= MaxBlock. Every read and write is bounds-checked; malformed or oversize input
// returns an error, never panics, and never allocates more than the caller's limit.
//
// Stopgap note (Core-Deps-PARENA-First): this decoder is plain Go. The format itself is PARENA's own
// compress/lz4_block.prn (verified against liblz4 both ways); testdata/*.lz4 are vectors produced by
// that PARENA compressor. Binding the PARENA-compiled decoder directly over cgo clashes with
// BRAWLPIT's own minimal runtime symbols in this binary, so a Go decoder is the interim.
package hslz4

import (
	"encoding/binary"
	"errors"
)

// MaxBlock is the largest raw size of one frame (PARENA's encoder indexes up to 65535 bytes).
const MaxBlock = 65535

var ErrCorrupt = errors.New("hslz4: corrupt input")

// DecodeBlock decodes one LZ4 block that must expand to exactly outLen bytes.
func DecodeBlock(src []byte, outLen int) ([]byte, error) {
	if outLen < 0 || outLen > MaxBlock {
		return nil, ErrCorrupt
	}
	dst := make([]byte, 0, outLen)
	i := 0
	for i < len(src) {
		tok := src[i]
		i++
		ll := int(tok >> 4)
		if ll == 15 {
			for {
				if i >= len(src) {
					return nil, ErrCorrupt
				}
				b := src[i]
				i++
				ll += int(b)
				if ll > outLen {
					return nil, ErrCorrupt
				}
				if b != 255 {
					break
				}
			}
		}
		if ll > len(src)-i || ll > outLen-len(dst) {
			return nil, ErrCorrupt
		}
		dst = append(dst, src[i:i+ll]...)
		i += ll
		if i == len(src) { // final sequence carries literals only
			break
		}
		if i+2 > len(src) {
			return nil, ErrCorrupt
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		if off == 0 || off > len(dst) {
			return nil, ErrCorrupt
		}
		ml := int(tok & 15)
		if ml == 15 {
			for {
				if i >= len(src) {
					return nil, ErrCorrupt
				}
				b := src[i]
				i++
				ml += int(b)
				if ml > outLen {
					return nil, ErrCorrupt
				}
				if b != 255 {
					break
				}
			}
		}
		ml += 4
		if ml > outLen-len(dst) {
			return nil, ErrCorrupt
		}
		for k := 0; k < ml; k++ { // byte-wise: matches may overlap their own output
			dst = append(dst, dst[len(dst)-off])
		}
	}
	if len(dst) != outLen {
		return nil, ErrCorrupt
	}
	return dst, nil
}

// DecodeFrames decodes a body of frames (see the package comment), refusing to produce more than
// maxTotal bytes.
func DecodeFrames(body []byte, maxTotal int) ([]byte, error) {
	var out []byte
	for len(body) > 0 {
		if len(body) < 8 {
			return nil, ErrCorrupt
		}
		raw := int(binary.LittleEndian.Uint32(body[0:4]))
		comp := int(binary.LittleEndian.Uint32(body[4:8]))
		body = body[8:]
		if raw > MaxBlock || comp < 0 || comp > len(body) || len(out)+raw > maxTotal {
			return nil, ErrCorrupt
		}
		blk, err := DecodeBlock(body[:comp], raw)
		if err != nil {
			return nil, err
		}
		out = append(out, blk...)
		body = body[comp:]
	}
	return out, nil
}
