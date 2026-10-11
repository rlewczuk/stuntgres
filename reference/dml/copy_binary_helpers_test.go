package reference_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"stuntgres/reference/fixture"
)

// Handwritten wire builders use only the documented network-byte-order format.
// No expected frame is obtained from a backend's COPY output.
func copyBinaryHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
func copyBinary16(n int16) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(n))
	return b[:]
}
func copyBinary32(n int32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(n))
	return b[:]
}
func copyBinary64(n int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	return b[:]
}
func copyBinaryHeader(flags, extensionLength int32, extension []byte) []byte {
	b := copyBinaryHex("5047434f50590aff0d0a00")
	b = append(b, copyBinary32(flags)...)
	b = append(b, copyBinary32(extensionLength)...)
	return append(b, extension...)
}
func copyBinaryRow(fields ...[]byte) []byte {
	b := copyBinary16(int16(len(fields)))
	for _, field := range fields {
		if field == nil {
			b = append(b, copyBinary32(-1)...)
			continue
		}
		b = append(b, copyBinary32(int32(len(field)))...)
		b = append(b, field...)
	}
	return b
}
func copyBinaryStream(rows ...[]byte) []byte {
	b := copyBinaryHeader(0, 0, nil)
	for _, row := range rows {
		b = append(b, row...)
	}
	return append(b, copyBinary16(-1)...)
}

func copyBinaryScenario(t *testing.T, body func(*testing.T, copyTextConnection)) {
	t.Helper()
	copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
		body(t, c)
	})
}

func copyBinaryCount(t *testing.T, c copyTextConnection, want int64) {
	t.Helper()
	var count int64
	if err := c.conn.QueryRowContext(c.ctx, "SELECT count(*) FROM copy_binary_probe").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Errorf("published row count=%d want=%d", count, want)
	}
}

func TestReferenceCopyBinaryRoundTrip(t *testing.T) {
	copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_binary_rows(i int4,t text,b bool)")
		// Literal acceptance bytes: two rows, NULL distinct from empty string.
		input := copyBinaryHex("5047434f50590aff0d0a0000000000000000000003000000040000002a00000002686900000001010003ffffffff000000000000000100ffff")
		reader := &copyTextChunks{text: string(input), widths: []int{1, 3, 7}}
		result, err := c.from("COPY copy_binary_rows FROM STDIN WITH(FORMAT binary)", reader)
		copyTextSuccess(t, result, err, 2)
		c.selectRows(t, "SELECT * FROM copy_binary_rows ORDER BY i NULLS LAST", []string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(42), "hi", true}, {nil, "", false}})
		c.output(t, "COPY (SELECT * FROM copy_binary_rows ORDER BY i NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(input), 2)
		result, err = c.from("COPY copy_binary_rows FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(nil, nil, nil))))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT * FROM copy_binary_rows WHERE i IS NULL AND t IS NULL", []string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{nil, nil, nil}})
		c.output(t, "COPY (SELECT * FROM copy_binary_rows WHERE i IS NULL AND t IS NULL) TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow(nil, nil, nil))), 1)
		c.exec(t, "TRUNCATE copy_binary_rows")
		c.output(t, "COPY copy_binary_rows TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream()), 0)
	})
}
