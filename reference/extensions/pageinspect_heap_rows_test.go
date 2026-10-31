package reference_test

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapRows(t *testing.T) {
	t.Run("images-and-parameters", func(t *testing.T) {
		heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			large := make([]byte, 8192)
			copy(large, heapImage())
			row := heapRow(b.PG)
			heapHeld(t, p, b, c, heapSource, []fixture.Step{heapStep(heapSource, heapImage(), row), heapStep(heapSource, large, row), heapStep(heapSource, nil), heapStep(heapSource, heapImage(), row)})
			// Three bounded pointers share the same immutable tuple. Flags do not
			// determine storage, and ORDER BY is explicit for the slot sequence.
			multi := make([]byte, 108)
			copy(multi[72:], heapImage()[32:])
			binary.LittleEndian.PutUint16(multi[12:], 40)
			var want [][]any
			for i, flag := range []uint32{1, 0, 2, 3} {
				heapPointer(multi, i, 72, flag, 36)
				r := heapRow(b.PG)
				r[0], r[1], r[2] = int64(i+1), int64(72), int64(flag)
				want = append(want, r)
			}
			heapHeld(t, p, b, c, heapSource+" ORDER BY lp", []fixture.Step{heapStep(heapSource+" ORDER BY lp", multi, want...)})
			// After publication neither caller input nor exported byte payloads own
			// the table's backing. Assert again from independently literal data.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE heap_bytes(raw bytea)", 0))
			input := heapImage()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO heap_bytes VALUES($1)", 1, input))
			clear(input)
			var exported []byte
			if err := c.QueryRowContext(p.Ctx, "SELECT t_data FROM heap_bytes CROSS JOIN LATERAL heap_part.heap_page_items(raw)").Scan(&exported); err != nil {
				t.Fatal(err)
			}
			if string(exported) != string([]byte{0, 0x80, 0xff, 0x5c}) {
				t.Fatal("raw payload", exported)
			}
			clear(exported)
			heapBoth(t, p, b, c, fixture.Select("SELECT * FROM heap_part.heap_page_items((SELECT raw FROM heap_bytes))", heapNames, heapTypes, [][]any{heapRow(b.PG)}))
		})
	})
	t.Run("source-boundaries", heapBoundaries)
}

func heapBoundaries(t *testing.T) {
	heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, n := range []int{0, 1, 23} {
			heapHeld(t, p, b, c, heapSource, []fixture.Step{heapError(heapSource, "22023", fmt.Sprintf("input page too small (%d bytes)", n), make([]byte, n))})
		}
		for _, n := range []int{24, 25, 8192, 8193} {
			heapBoth(t, p, b, c, heapStep(heapSource, make([]byte, n)))
		}
		for _, lower := range []uint16{0, 24, 25, 27, 28, 31} {
			raw := heapImage()
			binary.LittleEndian.PutUint16(raw[12:], lower)
			var rows [][]any
			if lower >= 28 {
				rows = [][]any{heapRow(b.PG)}
			}
			heapBoth(t, p, b, c, heapStep(heapSource, raw, rows...))
		}
		for _, tc := range []struct {
			offset int
			value  uint16
		}{{10, 65535}, {14, 0}, {14, 65535}, {16, 1}, {16, 65535}, {18, 65535}, {18, 0}} {
			raw := heapImage()
			binary.LittleEndian.PutUint16(raw[tc.offset:], tc.value)
			heapBoth(t, p, b, c, heapStep(heapSource, raw, heapRow(b.PG)))
		}
		// A page header with nonsensical order, special, size/version and new
		// page marker is intentionally decoded rather than generically rejected.
		corrupt := heapImage()
		binary.LittleEndian.PutUint16(corrupt[14:], 0)
		binary.LittleEndian.PutUint16(corrupt[16:], 1)
		binary.LittleEndian.PutUint16(corrupt[18:], 0xffff)
		heapBoth(t, p, b, c, heapStep(heapSource, corrupt, heapRow(b.PG)))
		for _, flag := range []uint32{0, 1, 2, 3} {
			raw, row := heapImage(), heapRow(b.PG)
			heapPointer(raw, 0, 32, flag, 36)
			row[2] = int64(flag)
			heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		}
		for _, tc := range []struct{ off, size uint32 }{{32, 0}, {32, 23}, {33, 35}, {40, 36}, {32767, 32767}} {
			raw, row := heapImage(), make([]any, 14)
			heapPointer(raw, 0, tc.off, 1, tc.size)
			row[0], row[1], row[2], row[3] = int64(1), int64(tc.off), int64(1), int64(tc.size)
			heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		}
		for _, hoff := range []byte{0, 23, 25, 40, 255} {
			raw, row := heapImage(), heapRow(b.PG)
			raw[54], row[10], row[11], row[12], row[13] = hoff, int64(hoff), nil, nil, nil
			heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		}
		// Aligned minimum header: no bitmap/OID, data starts at byte 24.
		raw, row := heapImage(), heapRow(b.PG)
		raw[54] = 24
		binary.LittleEndian.PutUint16(raw[52:], 0x0300)
		row[9], row[10], row[11], row[12], row[13] = int64(768), int64(24), nil, nil, string(raw[56:68])
		heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		raw, row = heapImage()[:64], heapRow(b.PG)
		heapPointer(raw, 0, 32, 1, 32)
		row[3], row[13] = int64(32), ""
		heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		for _, tc := range []struct {
			mask      uint16
			bits, oid bool
		}{{0x0300, false, false}, {0x0301, true, false}, {0x0308, false, true}} {
			raw, row = heapImage(), heapRow(b.PG)
			binary.LittleEndian.PutUint16(raw[52:], tc.mask)
			row[9] = int64(tc.mask)
			if !tc.bits {
				row[11] = nil
			}
			if !tc.oid {
				row[12] = nil
			}
			heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		}
		for _, tc := range []struct {
			natts uint16
			bits  any
		}{{0x8000, ""}, {0x8009, "1000000110000000"}, {0x87ff, nil}} {
			raw, row = heapImage(), heapRow(b.PG)
			binary.LittleEndian.PutUint16(raw[50:], tc.natts)
			row[8], row[11] = int64(tc.natts), tc.bits
			heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		}
		for _, field := range []uint32{0x80000000, 0xffffffff} {
			raw, row = heapImage(), heapRow(b.PG)
			binary.LittleEndian.PutUint32(raw[32:], 0xffffffff)
			binary.LittleEndian.PutUint32(raw[36:], 0xffffffff)
			binary.LittleEndian.PutUint32(raw[40:], field)
			binary.LittleEndian.PutUint32(raw[60:], 0xffffffff)
			binary.LittleEndian.PutUint16(raw[44:], 0xffff)
			binary.LittleEndian.PutUint16(raw[46:], 0xffff)
			binary.LittleEndian.PutUint16(raw[48:], 0xffff)
			binary.LittleEndian.PutUint16(raw[50:], 0xffff)
			binary.LittleEndian.PutUint16(raw[52:], 0xffff)
			row[4], row[5], row[6], row[7], row[8], row[9], row[11] = "4294967295", "4294967295", int64(int32(field)), "(4294967295,65535)", int64(65535), int64(65535), nil
			row[12] = int64(4294967295)
			if b.PG {
				row[12] = "4294967295"
			}
			heapBoth(t, p, b, c, heapStep(heapSource, raw, row))
		}
		zero := heapImage()
		clear(zero[44:50])
		row = heapRow(b.PG)
		row[7] = "(0,0)"
		heapBoth(t, p, b, c, heapStep(heapSource, zero, row))
	})
}
