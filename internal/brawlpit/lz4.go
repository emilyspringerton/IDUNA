package brawlpit

// lz4.go — S417-03, founder real-time: "PARENA has lz4" / "you can build parena in." A real cgo
// binding over this package's own PARENA-compiled LZ4-style compressor (lz4_gen.c, compiled from
// PARENA's stdlib/compress/lz4.prn -- see lz4gen_README.md), used to compress level payloads
// over the wire (S417-02's public API) instead of a Go LZ4 library or a hand-rolled Go
// implementation -- reusing the exact same real codec BRAWLPIT's native client links directly
// (packages/common/lz4/, a byte-for-byte copy of this package's own lz4_*.c/.h files), so both
// ends of the wire speak the identical compression format. The .c files live directly in this
// package directory (not a subdirectory) because cgo only auto-compiles sibling .c files, not
// ones nested a level down.

/*
#include "lz4_wrapper.h"
#include <stdlib.h>
*/
import "C"
import "unsafe"

// CompressLZ4 compresses data using the PARENA-compiled codec, returning a new, independent byte
// slice -- never larger than len(data)+1 (a single flag byte), thanks to lz4_wrapper.c's own
// real STORED fallback for incompressible input.
func CompressLZ4(data []byte) []byte {
	var inPtr *C.uchar
	if len(data) > 0 {
		inPtr = (*C.uchar)(unsafe.Pointer(&data[0]))
	}
	var outPtr *C.uchar
	n := C.pw_lz4_compress(inPtr, C.long(len(data)), &outPtr)
	return collectLZ4Result(n, outPtr)
}

// DecompressLZ4 reverses CompressLZ4. Returns nil if data is malformed (too short, or an
// unrecognized format flag byte) -- the C wrapper's own real "-1 on failure" contract.
func DecompressLZ4(data []byte) []byte {
	var inPtr *C.uchar
	if len(data) > 0 {
		inPtr = (*C.uchar)(unsafe.Pointer(&data[0]))
	}
	var outPtr *C.uchar
	n := C.pw_lz4_decompress(inPtr, C.long(len(data)), &outPtr)
	return collectLZ4Result(n, outPtr)
}

// collectLZ4Result copies a malloc'd C buffer (freeing it afterward) into a real, independent Go
// byte slice -- the shared tail both CompressLZ4/DecompressLZ4 need after their own real C call.
func collectLZ4Result(n C.long, outPtr *C.uchar) []byte {
	if n < 0 {
		return nil
	}
	defer C.free(unsafe.Pointer(outPtr))

	out := make([]byte, int(n))
	if n > 0 {
		copy(out, unsafe.Slice((*byte)(unsafe.Pointer(outPtr)), int(n)))
	}
	return out
}
