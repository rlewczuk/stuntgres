package reference_test

import (
	"database/sql"
	"encoding/binary"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapAttrsSplit(t *testing.T) {
	t.Run("physical-and-null-slots", func(t *testing.T) {
		attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				data        string
				mask, natts int64
				bits        any
				want        string
			}{
				{"0100000100020200", 0, 2, nil, attrsArray("01000001", "00020200")},
				{"01000001", 1, 2, "10000000", attrsArray("01000001", "NULL")},
				{"01000001", 0, 1, nil, attrsArray("01000001", "NULL")},
				{"", 1, 2, "00000000", attrsArray("NULL", "NULL")},
				{"0100000100020200", -65536, 65538, nil, attrsArray("01000001", "00020200")},
			} {
				attrsSplitRoutes(t, p, b, c, attrsBytes(tc.data), tc.mask, tc.natts, tc.bits, tc.want)
			}
		})
	})
	t.Run("empty-descriptor", func(t *testing.T) {
		attrsCase(t, "", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			attrsSplitRoutes(t, p, b, c, []byte{}, int64(0), int64(0), nil, "{}")
			heapBoth(t, p, b, c, heapError(attrsSplit5, "XX001", "end of tuple reached without looking at all its data", []byte{0}, 0, 0, nil))
		})
	})
}

func TestReferencePageinspectHeapAttrsRows(t *testing.T) {
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		data := attrsBytes("0100000100020200")
		for _, full := range []bool{false, true} {
			image := attrsImage(data, 0, 2, nil, full)
			off := int64(32)
			if full {
				off = 8160
			}
			row := attrsRow(off, 32, 0, 2, 24, nil, attrsArray("01000001", "00020200"))
			heapHeld(t, p, b, c, attrsSource2, []fixture.Step{attrsRows(attrsSource2, [][]any{row}, image), attrsRows(attrsSource2, nil, nil), attrsRows(attrsSource2, nil, make([]byte, 24))})
			heapHeld(t, p, b, c, attrsSource3, []fixture.Step{attrsRows(attrsSource3, [][]any{row}, image, false), attrsRows(attrsSource3, [][]any{row}, image, true)})
		}
		for _, tc := range []struct{ off, flags, length uint32 }{{32767, 1, 32}, {32, 0, 0}, {32, 2, 0}, {32, 3, 0}, {32, 1, 22}} {
			image := attrsImage(data, 0, 2, nil, false)
			heapPointer(image, 0, tc.off, tc.flags, tc.length)
			row := make([]any, 14)
			row[0], row[1], row[2], row[3] = int64(1), int64(tc.off), int64(tc.flags), int64(tc.length)
			q := "SELECT * FROM heap_part.heap_page_item_attrs($1,4294967295::oid::regclass) ORDER BY lp"
			heapBoth(t, p, b, c, attrsRows(q, [][]any{row}, image))
		}
		bad := attrsImage(data, 0, 2, nil, false)
		bad[54] = 23
		row := attrsRow(32, 32, 0, 2, 23, nil, nil)
		heapBoth(t, p, b, c, attrsRows(attrsSource2, [][]any{row}, bad))
		// Heap decoding does not reject page version/type bytes.
		image := attrsImage(data, 0, 2, nil, false)
		binary.LittleEndian.PutUint16(image[18:], 1)
		heapBoth(t, p, b, c, attrsRows(attrsSource2, [][]any{attrsRow(32, 32, 0, 2, 24, nil, attrsArray("01000001", "00020200"))}, image))
		q := "SELECT * FROM heap_part.heap_page_item_attrs($1,4294967295::oid::regclass) ORDER BY lp"
		heapBoth(t, p, b, c, attrsRows(q, nil, make([]byte, 24)))
		// A valid zero-byte payload still reaches the descriptor, unlike NULL data.
		empty := attrsImage([]byte{}, 0, 0, nil, false)
		heapBoth(t, p, b, c, attrsRows(attrsSource2, [][]any{attrsRow(32, 24, 0, 0, 24, nil, attrsArray("NULL", "NULL"))}, empty))
		multiple := make([]byte, 104)
		binary.LittleEndian.PutUint16(multiple[12:], 32)
		binary.LittleEndian.PutUint16(multiple[14:], 40)
		binary.LittleEndian.PutUint16(multiple[16:], 104)
		binary.LittleEndian.PutUint16(multiple[18:], 0x2004)
		heapPointer(multiple, 0, 40, 1, 32)
		heapPointer(multiple, 1, 72, 1, 32)
		tuple := attrsImage(data, 0, 2, nil, false)[32:]
		copy(multiple[40:], tuple)
		copy(multiple[72:], tuple)
		first := attrsRow(40, 32, 0, 2, 24, nil, attrsArray("01000001", "00020200"))
		second := attrsRow(72, 32, 0, 2, 24, nil, attrsArray("01000001", "00020200"))
		second[0] = int64(2)
		heapBoth(t, p, b, c, attrsRows(attrsSource2, [][]any{first, second}, multiple))
		// Two independent identical images retain multiplicity, not traversal order.
		q = "SELECT h.* FROM (VALUES($1::bytea),($1::bytea)) AS v(raw) CROSS JOIN LATERAL heap_part.heap_page_item_attrs(v.raw,'attrs_schema'::regclass) h"
		r := attrsRow(32, 32, 0, 2, 24, nil, attrsArray("01000001", "00020200"))
		s := attrsRows(q, [][]any{r, r}, attrsImage(data, 0, 2, nil, false))
		s.Bag = true
		heapBoth(t, p, b, c, s)
	})
}
