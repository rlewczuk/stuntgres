package enginetest

import (
	"encoding/binary"

	"stuntgres/internal/engine/core"
)

// ProtocolReader is a session.ProtocolInput over Data, for tests that exercise
// staged engine consumption of Bind and fastpath messages without SQL or a
// fake backend. Position counts the bytes consumed.
type ProtocolReader struct {
	Data     []byte
	Position int
}

// Bytes consumes the next n bytes.
func (r *ProtocolReader) Bytes(n int) ([]byte, error) {
	if n < 0 || n > len(r.Data)-r.Position {
		return nil, core.SQLError("08P01", "insufficient data left in message")
	}
	p := r.Data[r.Position : r.Position+n]
	r.Position += n
	return p, nil
}

// Uint16 consumes a big-endian uint16.
func (r *ProtocolReader) Uint16() (uint16, error) {
	p, e := r.Bytes(2)
	if e != nil {
		return 0, e
	}
	return binary.BigEndian.Uint16(p), nil
}

// Int32 consumes a big-endian int32.
func (r *ProtocolReader) Int32() (int32, error) {
	p, e := r.Bytes(4)
	if e != nil {
		return 0, e
	}
	return int32(binary.BigEndian.Uint32(p)), nil
}

// End requires that every byte was consumed.
func (r *ProtocolReader) End() error {
	if r.Position != len(r.Data) {
		return core.SQLError("08P01", "invalid message format")
	}
	return nil
}
