package engine

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"stuntgres/internal/sqlstate"
)

const copyBinarySignature = "PGCOPY\n\xff\r\n\x00"

// copyBinaryReader is the binary COPY codec: it checks the file header and
// frames tuples and fields. It reuses its buffers across rows and knows
// nothing of the statement executing the COPY.
type copyBinaryReader struct {
	reader        *bufio.Reader
	scratch       [4096]byte
	payload       []byte
	number        int64
	header, ended bool
}

func (r *copyBinaryReader) readFixed(n int) ([]byte, error) {
	_, err := io.ReadFull(r.reader, r.scratch[:n])
	return r.scratch[:n], err
}
func binaryEOF(err error) bool { return err == io.EOF || err == io.ErrUnexpectedEOF }
func (r *copyBinaryReader) headerInput() error {
	p, err := r.readFixed(len(copyBinarySignature))
	if err != nil {
		if !binaryEOF(err) {
			return err
		}
		return sqlError("22P04", "COPY file signature not recognized")
	}
	if string(p) != copyBinarySignature {
		return sqlError("22P04", "COPY file signature not recognized")
	}
	p, err = r.readFixed(4)
	if err != nil {
		if !binaryEOF(err) {
			return err
		}
		return sqlError("22P04", "invalid COPY file header (missing flags)")
	}
	flags := binary.BigEndian.Uint32(p)
	if flags&0x10000 != 0 {
		return sqlError("22P04", "invalid COPY file header (WITH OIDS)")
	}
	if flags&0xffff0000 != 0 {
		return sqlError("22P04", "unrecognized critical flags in COPY file header")
	}
	p, err = r.readFixed(4)
	if err != nil {
		if !binaryEOF(err) {
			return err
		}
		return sqlError("22P04", "invalid COPY file header (missing length)")
	}
	length := int32(binary.BigEndian.Uint32(p))
	if length < 0 {
		return sqlError("22P04", "invalid COPY file header (missing length)")
	}
	for length > 0 {
		n := min(int(length), len(r.scratch))
		if _, err = r.readFixed(n); err != nil {
			if !binaryEOF(err) {
				return err
			}
			return sqlError("22P04", "invalid COPY file header (wrong length)")
		}
		length -= int32(n)
	}
	return nil
}

// row starts the next tuple and returns its field count, or ok=false at the
// end of data. Every started tuple advances the line number.
func (r *copyBinaryReader) row() (count int16, ok bool, err error) {
	if r.ended {
		return 0, false, nil
	}
	r.number++
	p, err := r.readFixed(2)
	if err != nil {
		if binaryEOF(err) {
			r.ended = true
			return 0, false, nil
		}
		return 0, false, err
	}
	count = int16(binary.BigEndian.Uint16(p))
	if count != -1 {
		return count, true, nil
	}
	r.ended = true
	_, err = r.reader.ReadByte()
	if err == io.EOF {
		return 0, false, nil
	}
	if err == nil {
		err = sqlError("22P04", "received copy data after EOF marker")
	}
	return 0, false, err
}

// field reads the next field of the tuple: its payload, valid until the next
// call, or null=true for a NULL.
func (r *copyBinaryReader) field() (payload []byte, null bool, err error) {
	p, err := r.readFixed(4)
	if err != nil {
		if binaryEOF(err) {
			err = sqlError("22P04", "unexpected EOF in COPY data")
		}
		return nil, false, err
	}
	length := int32(binary.BigEndian.Uint32(p))
	if length == -1 {
		return nil, true, nil
	}
	if length < -1 {
		return nil, false, sqlError("22P04", "invalid field size")
	}
	if length >= 1073741823 {
		return nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "string buffer exceeds maximum allowed length (1073741823 bytes)", Detail: fmt.Sprintf("Cannot enlarge string buffer containing 0 bytes by %d more bytes.", length)}
	}
	// Grow with real bytes, not an attacker-controlled unfulfilled declaration.
	r.payload = r.payload[:0]
	for remaining := int(length); remaining > 0; {
		n := min(remaining, len(r.scratch))
		p, err = r.readFixed(n)
		if err != nil {
			if binaryEOF(err) {
				err = sqlError("22P04", "unexpected EOF in COPY data")
			}
			return nil, false, err
		}
		r.payload = append(r.payload, p...)
		remaining -= n
	}
	return r.payload, false, nil
}
