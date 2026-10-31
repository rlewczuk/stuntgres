package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapAttrsDetoast(t *testing.T) {
	for _, tc := range []struct{ name, raw, payload string }{
		{"short", "076162", "6162"},
		{"plain", "180000006162", "6162"},
		{"pglz", "320000000300000000616263", "616263"},
		{"lz4", "320000000300004030616263", "616263"},
		{"short-empty", "03", ""},
		{"plain-empty", "10000000", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attrsCase(t, "v text", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				data := attrsBytes(tc.raw)
				attrsSplitRoutes(t, p, b, c, data, 0, 1, nil, attrsArray(tc.raw))
				heapHeld(t, p, b, c, attrsSplit6, []fixture.Step{attrsValue(attrsSplit6, attrsArray(tc.payload), data, 0, 1, nil, true)})
				image := attrsImage(data, 0, 1, nil, false)
				heapBoth(t, p, b, c, attrsRows(attrsSource2, [][]any{attrsRow(32, int64(24+len(data)), 0, 1, 24, nil, attrsArray(tc.raw))}, image))
				for _, detoast := range []bool{false, true} {
					want := tc.raw
					if detoast {
						want = tc.payload
					}
					heapBoth(t, p, b, c, attrsRows(attrsSource3, [][]any{attrsRow(32, int64(24+len(data)), 0, 1, 24, nil, attrsArray(want))}, image, detoast))
				}
			})
		})
	}
	t.Run("packed-alignment", func(t *testing.T) {
		attrsCase(t, "a boolean,v text,z integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ raw, physical string }{{"0107616201020304", "076162"}, {"01000000180000006162000001020304", "180000006162"}} {
				attrsSplitRoutes(t, p, b, c, attrsBytes(tc.raw), 0, 3, nil, attrsArray("01", tc.physical, "01020304"))
				heapBoth(t, p, b, c, attrsValue(attrsSplit6, attrsArray("01", "6162", "01020304"), attrsBytes(tc.raw), 0, 3, nil, true))
			}
		})
	})
	t.Run("safe-corruption", func(t *testing.T) {
		attrsCase(t, "v text", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ raw, code, message string }{
				{"260000000300000000", "XX001", "compressed pglz data is corrupt"},
				{"260000000300004030", "XX001", "compressed lz4 data is corrupt"},
				{"260000000300008000", "XX000", "invalid compression method id 2"},
				{"010000000000000000000000000000000000", "XX001", "first byte of varlena attribute is incorrect for attribute 0"},
			} {
				heapBoth(t, p, b, c, heapError(attrsSplit6, tc.code, tc.message, attrsBytes(tc.raw), 0, 1, nil, true))
			}
			// A pointer supplies identifiers, not the referenced TOAST content.
			pointer := attrsBytes("0112070000000300000001000000ffffffff")
			attrsSplitRoutes(t, p, b, c, pointer, 0, 1, nil, attrsArray("0112070000000300000001000000ffffffff"))
			// Source prediction; root must resolve full diagnostics on pinned 18.6.
			heapBoth(t, p, b, c, heapError(attrsSplit6, "XX000", "could not open relation with OID 4294967295", pointer, 0, 1, nil, true))
		})
	})
}
