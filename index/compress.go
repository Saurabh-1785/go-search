package index

import (
	"encoding/binary"
	"fmt"
)

// --- Delta + Varint Compressed Postings ---
//
// Standard format (before compression):
//   DocID0 (4 bytes) | TF0 (4 bytes) | DocID1 (4 bytes) | TF1 (4 bytes) | ...
//   = 8 bytes per posting
//
// Compressed format:
//   delta(DocID0) as varint | TF0 as varint | delta(DocID1) as varint | TF1 as varint | ...
//
// Delta encoding: posting lists are sorted by DocID. Instead of storing
// absolute DocIDs, store the difference from the previous DocID:
//   DocIDs:  [3, 7, 8, 100]
//   Deltas:  [3, 4, 1, 92]   (first delta = first DocID)
//
// Why? Deltas are small numbers → varint encodes them in fewer bytes.
//
// Varint: Uses Go's standard library binary.PutUvarint / binary.Uvarint.
// Same encoding as Protocol Buffers (LEB128).
//
// Compression ratio example (1000 postings, DocIDs 0..9999):
//   Uncompressed: 1000 × 8 = 8,000 bytes
//   Compressed:   ~1000 × (1+1) ≈ 2,000 bytes (avg delta ~10, TF ~1-5)
//   Ratio: ~4× compression

// EncodePostings delta+varint encodes a posting list.
// The postings MUST be sorted by DocID before calling this.
// Returns the compressed byte slice.
//
// Uses binary.PutUvarint from the standard library — no custom varint.
func EncodePostings(postings []Posting) []byte {
	if len(postings) == 0 {
		return nil
	}

	// Reasonable initial estimate: 4 bytes per posting on average.
	buf := make([]byte, 0, len(postings)*4)
	tmp := make([]byte, binary.MaxVarintLen64)

	var prevDocID uint32

	for _, p := range postings {
		// Delta encode DocID.
		delta := uint64(p.DocID - prevDocID)
		prevDocID = p.DocID

		// Write delta as varint using stdlib.
		n := binary.PutUvarint(tmp, delta)
		buf = append(buf, tmp[:n]...)

		// Write TF as varint using stdlib.
		n = binary.PutUvarint(tmp, uint64(p.TF))
		buf = append(buf, tmp[:n]...)
	}

	return buf
}

// DecodePostings reads a delta+varint encoded posting list.
// count is the number of postings to decode (from DictEntry.DF).
//
// Uses binary.Uvarint from the standard library — no custom varint.
func DecodePostings(data []byte, count int) ([]Posting, error) {
	if count == 0 || len(data) == 0 {
		return nil, nil
	}

	postings := make([]Posting, 0, count)
	pos := 0

	var prevDocID uint64

	for i := 0; i < count; i++ {
		if pos >= len(data) {
			return nil, fmt.Errorf("decode posting %d: unexpected end of data at byte %d", i, pos)
		}

		// Read delta-encoded DocID using stdlib.
		delta, n := binary.Uvarint(data[pos:])
		if n <= 0 {
			return nil, fmt.Errorf("decode posting %d docID: varint error (n=%d)", i, n)
		}
		pos += n

		// Read TF using stdlib.
		tf, n := binary.Uvarint(data[pos:])
		if n <= 0 {
			return nil, fmt.Errorf("decode posting %d TF: varint error (n=%d)", i, n)
		}
		pos += n

		prevDocID += delta
		postings = append(postings, Posting{
			DocID: uint32(prevDocID),
			TF:    uint32(tf),
		})
	}

	return postings, nil
}
