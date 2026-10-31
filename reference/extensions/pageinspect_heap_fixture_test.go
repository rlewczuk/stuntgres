package reference_test

import (
	"database/sql"
	"encoding/binary"
	"testing"

	"stuntgres/reference/fixture"
)

const heapSource = "SELECT * FROM heap_part.heap_page_items($1)"
const heapFlagsSource = "SELECT * FROM heap_part.heap_tuple_infomask_flags($1,$2)"
const heapFlagsScalar = "SELECT heap_part.heap_tuple_infomask_flags($1,$2)"

var heapNames = []string{"lp", "lp_off", "lp_flags", "lp_len", "t_xmin", "t_xmax", "t_field3", "t_ctid", "t_infomask2", "t_infomask", "t_hoff", "t_bits", "t_oid", "t_data"}
var heapTypes = []string{"INT2", "INT2", "INT2", "INT2", "XID", "XID", "INT4", "TID", "INT4", "INT4", "INT2", "TEXT", "OID", "BYTEA"}
var heapFlagsNames = []string{"raw_flags", "combined_flags"}
var heapFlagsTypes = []string{"_TEXT", "_TEXT"}

// Independently assembled REL_18_6 amd64 PageHeaderData/ItemIdData/HeapTupleHeaderData.
// These are caller bytes, never acquired pages or bytes synthesized from SQL rows.
func heapImage() []byte {
	p := make([]byte, 68)
	binary.LittleEndian.PutUint16(p[12:], 28)
	heapPointer(p, 0, 32, 1, 36)
	binary.LittleEndian.PutUint32(p[32:], 0x89abcdef)
	binary.LittleEndian.PutUint32(p[36:], 0xfedcba98)
	binary.LittleEndian.PutUint32(p[40:], 0xfffffffe)
	binary.LittleEndian.PutUint16(p[44:], 1)
	binary.LittleEndian.PutUint16(p[46:], 2)
	binary.LittleEndian.PutUint16(p[48:], 513)
	binary.LittleEndian.PutUint16(p[50:], 0x8009)
	binary.LittleEndian.PutUint16(p[52:], 0x0309)
	p[54], p[55], p[56] = 32, 0x81, 0x01
	binary.LittleEndian.PutUint32(p[60:], 0xfedcba98)
	copy(p[64:], []byte{0, 0x80, 0xff, 0x5c})
	return p
}

func heapPointer(p []byte, slot int, off, flags, size uint32) {
	binary.LittleEndian.PutUint32(p[24+4*slot:], off|flags<<15|size<<17)
}

func heapRow(pg bool) []any {
	var oid any = int64(4275878552)
	if pg {
		oid = "4275878552"
	}
	return []any{int64(1), int64(32), int64(1), int64(36), "2309737967", "4275878552", int64(-2), "(65538,513)", int64(32777), int64(777), int64(32), "1000000110000000", oid, string([]byte{0, 0x80, 0xff, 0x5c})}
}

func heapCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP EXTENSION IF EXISTS pageinspect CASCADE", "DROP SCHEMA IF EXISTS heap_part CASCADE", "DROP SCHEMA IF EXISTS heap_moved CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			if install {
				for _, q := range []string{"CREATE SCHEMA heap_part", "CREATE EXTENSION pageinspect SCHEMA heap_part"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
			}
			body(t, p, b, c)
		})
	}
}

func heapBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func heapHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, steps []fixture.Step) {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	held := fixture.HeldStatement{Executor: c, Statement: st, SQL: query}
	for _, s := range steps {
		heapBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func heapStep(query string, raw any, rows ...[]any) fixture.Step {
	return fixture.Select(query, heapNames, heapTypes, rows, raw)
}

func heapFlagsStep(query string, a, b any, raw, combined any) fixture.Step {
	return fixture.Select(query, heapFlagsNames, heapFlagsTypes, [][]any{{raw, combined}}, a, b)
}

func heapError(query, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func heapLookup(query, message string, position int) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42883", Message: message, Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: fixture.Position(position)})
	s.SelectRows = true
	return s
}

func heapDeclaration(flags bool, name, schema, owner string) fixture.Step {
	nargs, args, source := int64(1), "17", "heap_page_items"
	allargs := "{17,21,21,21,21,28,28,23,27,23,23,21,25,26,17}"
	modes := "{i,o,o,o,o,o,o,o,o,o,o,o,o,o,o}"
	names := "{page,lp,lp_off,lp_flags,lp_len,t_xmin,t_xmax,t_field3,t_ctid,t_infomask2,t_infomask,t_hoff,t_bits,t_oid,t_data}"
	sets, rows := true, float64(1000)
	if flags {
		nargs, args, source = 2, "23 23", "heap_tuple_infomask_flags"
		allargs, modes, names = "{23,23,1009,1009}", "{i,i,o,o}", "{t_infomask,t_infomask2,raw_flags,combined_flags}"
		sets, rows = false, 0
	}
	return fixture.Select("SELECT p.proname::text AS name,n.nspname::text AS schema,r.rolname::text AS owner,p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proargtypes::text AS args,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.prorows AS rows,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.procost AS cost,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang WHERE p.proname='"+name+"' AND n.nspname='"+schema+"' AND p.proargtypes::text='"+args+"'", []string{"name", "schema", "owner", "nargs", "result", "args", "allargs", "modes", "names", "sets", "rows", "strict", "volatility", "parallel", "cost", "source", "binary", "language"}, []string{"TEXT", "TEXT", "TEXT", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "FLOAT4", "BOOL", "TEXT", "TEXT", "FLOAT4", "TEXT", "TEXT", "TEXT"}, [][]any{{name, schema, owner, nargs, int64(2249), args, allargs, modes, names, sets, rows, true, "v", "s", float64(1), source, "$libdir/pageinspect", "c"}})
}
