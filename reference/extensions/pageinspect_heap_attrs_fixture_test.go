package reference_test

import (
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const attrsSplit5 = "SELECT heap_part.tuple_data_split('attrs_schema'::regclass::oid,$1,$2,$3,$4) AS attrs"
const attrsSplit6 = "SELECT heap_part.tuple_data_split('attrs_schema'::regclass::oid,$1,$2,$3,$4,$5) AS attrs"
const attrsSource2 = "SELECT * FROM heap_part.heap_page_item_attrs($1,'attrs_schema'::regclass) ORDER BY lp"
const attrsSource3 = "SELECT * FROM heap_part.heap_page_item_attrs($1,'attrs_schema'::regclass,$2) ORDER BY lp"

var attrsNames = []string{"lp", "lp_off", "lp_flags", "lp_len", "t_xmin", "t_xmax", "t_field3", "t_ctid", "t_infomask2", "t_infomask", "t_hoff", "t_bits", "t_oid", "t_attrs"}
var attrsTypes = []string{"INT2", "INT2", "INT2", "INT2", "XID", "XID", "INT4", "TID", "INT4", "INT4", "INT2", "TEXT", "OID", "_BYTEA"}

// REL_18_6 physical bytes, not a projection of embedded relation contents.
func attrsImage(data []byte, mask, natts uint16, bits []byte, full bool) []byte {
	hoff := 24
	if len(bits) > 0 {
		hoff = (23 + len(bits) + 7) &^ 7
	}
	size, off := 32+hoff+len(data), 32
	if full {
		size = 8192
		off = size - hoff - len(data)
	}
	p := make([]byte, size)
	binary.LittleEndian.PutUint16(p[12:], 28)
	binary.LittleEndian.PutUint16(p[14:], uint16(off))
	binary.LittleEndian.PutUint16(p[16:], uint16(size))
	binary.LittleEndian.PutUint16(p[18:], 0x2004)
	heapPointer(p, 0, uint32(off), 1, uint32(hoff+len(data)))
	binary.LittleEndian.PutUint32(p[off:], 1)
	binary.LittleEndian.PutUint16(p[off+16:], 1)
	binary.LittleEndian.PutUint16(p[off+18:], natts)
	binary.LittleEndian.PutUint16(p[off+20:], mask)
	p[off+22] = byte(hoff)
	copy(p[off+23:], bits)
	copy(p[off+hoff:], data)
	return p
}

func attrsBytes(s string) []byte {
	v, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Expected text is constructed from literal expected attribute bytes, never by
// interpreting the supplied tuple or using the production array encoder.
func attrsArray(elements ...string) string {
	v := make([]string, len(elements))
	for i, e := range elements {
		v[i] = `"\\x` + e + `"`
		if e == "NULL" {
			v[i] = "NULL"
		}
	}
	return "{" + strings.Join(v, ",") + "}"
}

func attrsRow(off, length, mask, natts, hoff int64, bits any, array any) []any {
	return []any{int64(1), off, int64(1), length, "1", "0", int64(0), "(0,1)", natts, mask, hoff, bits, nil, array}
}

func attrsValue(query string, value any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"attrs"}, []string{"_BYTEA"}, [][]any{{value}}, args...)
}

func attrsRows(query string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(query, attrsNames, attrsTypes, rows, args...)
}

func attrsCase(t *testing.T, schema string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "heap_part", "heap_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			clean := func() {
				if !b.PG {
					return
				}
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP EXTENSION IF EXISTS pageinspect CASCADE", "DROP SCHEMA IF EXISTS heap_part CASCADE", "DROP SCHEMA IF EXISTS heap_moved CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(q, err)
					}
				}
			}
			clean()
			defer clean()
			for _, q := range []string{"CREATE SCHEMA heap_part", "CREATE EXTENSION pageinspect SCHEMA heap_part", "SET bytea_output TO hex", "CREATE TABLE attrs_schema(" + schema + ")"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func attrsSplitRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, data []byte, mask, natts any, bits any, expected any) {
	t.Helper()
	heapHeld(t, p, b, c, attrsSplit5, []fixture.Step{attrsValue(attrsSplit5, expected, data, mask, natts, bits)})
	heapHeld(t, p, b, c, attrsSplit6, []fixture.Step{attrsValue(attrsSplit6, expected, data, mask, natts, bits, false)})
}
