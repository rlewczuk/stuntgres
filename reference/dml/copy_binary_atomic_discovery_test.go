package reference_test

import (
	"bytes"
	"testing"
)

// The committed sentinel and valid first staged row make diagnostic discovery
// also an independent all-or-nothing acceptance definition.
func TestReferenceCopyBinaryDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, typ  string
		valid, bad []byte
		setup      []string
	}{
		{name: "int4_short", typ: "int4", valid: copyBinary32(99), bad: []byte{0}},
		{name: "int4_trailing", typ: "int4", valid: copyBinary32(99), bad: []byte{0, 0, 0, 42, 0}},
		{name: "text_nul", typ: "text", valid: []byte("sentinel"), bad: []byte{'a', 0, 'b'}},
		{name: "text_utf8", typ: "text", valid: []byte("sentinel"), bad: []byte{0xc3, 0x28}},
		{name: "jsonb_version", typ: "jsonb", valid: []byte{1, '{', '}'}, bad: []byte{2, '{', '}'}},
		{name: "jsonpath_version", typ: "jsonpath", valid: []byte{1, '$'}, bad: []byte{2, '$'}},
		{name: "array_oid", typ: "int4[]", valid: copyBinaryArray(23, [][2]int32{{1, 1}}, copyBinary32(99)), bad: copyBinaryArray(25, [][2]int32{{1, 1}}, copyBinary32(42))},
		{name: "array_short_child", typ: "int4[]", valid: copyBinaryArray(23, [][2]int32{{1, 1}}, copyBinary32(99)), bad: copyBinaryArray(23, [][2]int32{{1, 1}}, []byte{0})},
		{name: "composite_oid", typ: "copy_pair", setup: []string{"CREATE TYPE copy_pair AS(i int4,t text)"}, valid: copyBinaryComposite([]int32{23, 25}, copyBinary32(99), []byte("sentinel")), bad: copyBinaryComposite([]int32{25, 25}, copyBinary32(42), []byte("hi"))},
		{name: "range_short_bound", typ: "int4range", valid: copyBinaryRange(2, copyBinary32(99), copyBinary32(101)), bad: copyBinaryRange(2, []byte{0}, copyBinary32(3))},
		{name: "multirange_short", typ: "int4multirange", valid: copyBinaryMultirange(copyBinaryRange(2, copyBinary32(99), copyBinary32(101))), bad: append(append(copyBinary32(1), copyBinary32(10)...), 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				for _, sql := range tc.setup {
					c.exec(t, sql)
				}
				c.exec(t, "CREATE TABLE copy_binary_probe(v "+tc.typ+")")
				result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(tc.valid))))
				copyTextSuccess(t, result, err, 1)
				// Baseline is used only to detect mutation, never as a wire/value golden.
				var before bytes.Buffer
				result, err = c.to("COPY copy_binary_probe TO STDOUT WITH(FORMAT binary)", &before)
				copyTextSuccess(t, result, err, 1)
				result, err = c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(tc.valid), copyBinaryRow(tc.bad))))
				copyBinaryOutcome(t, "Diagnostics", tc.name, result, err, nil)
				copyBinaryCount(t, c, 1)
				var after bytes.Buffer
				result, err = c.to("COPY copy_binary_probe TO STDOUT WITH(FORMAT binary)", &after)
				copyTextSuccess(t, result, err, 1)
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Errorf("committed sentinel mutated: before=%x after=%x", before.Bytes(), after.Bytes())
				}
			})
		})
	}
	t.Run("earlier_receiver_before_later_field_framing", func(t *testing.T) {
		copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
			c.exec(t, "CREATE TABLE copy_binary_probe(i int4,t text)")
			c.exec(t, "INSERT INTO copy_binary_probe VALUES(99,'sentinel')")
			first := copyBinaryRow(copyBinary32(42), []byte("valid"))
			// Complete but short int4, then invalid negative length of the next field.
			bad := append(copyBinary16(2), copyBinary32(1)...)
			bad = append(bad, 0)
			bad = append(bad, copyBinary32(-2)...)
			result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(first, bad)))
			copyBinaryOutcome(t, "Diagnostics", "earlier_receiver_before_later_field_framing", result, err, nil)
			c.selectRows(t, "SELECT * FROM copy_binary_probe", []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		})
	})
}

func TestReferenceCopyBinaryFraming(t *testing.T) {
	copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_binary_probe(v int4)")
		row := copyBinaryRow(copyBinary32(42))
		for _, widths := range [][]int{{1}, {3, 1, 2, 7, 5}} {
			c.exec(t, "TRUNCATE copy_binary_probe")
			result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", &copyTextChunks{text: string(copyBinaryStream(row)), widths: widths})
			copyTextSuccess(t, result, err, 1)
			c.selectRows(t, "SELECT * FROM copy_binary_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
			c.output(t, "COPY copy_binary_probe TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(row)), 1)
		}
	})
}
