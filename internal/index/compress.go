package index

import (
	"encoding/binary"
	"fmt"
)

// EncodePostings delta-varint encodes a list of postings.
// The postings must be sorted by DocID.
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

// DecodePostings decodes a delta-varint encoded posting list.
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
