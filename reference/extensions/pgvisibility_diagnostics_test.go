package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityNullAndBounds(t *testing.T) {
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pvEmpty(t, p, b, c, "pv_part.heap")
		for _, block := range []int64{0, 1, 4294967294} { pvProbe(t, p, b, c, "pv_part.heap", block, false, false, false) }
		// Bounds do not allocate pages or a synthetic empty block.
		pvEmpty(t, p, b, c, "pv_part.heap")
		for _, spec := range []struct{ name string; names, types []string }{
			{"pg_visibility_map", pvMapNames, pvMapTypes}, {"pg_visibility", pvPageNames, pvPageTypes},
		} {
			q := "SELECT * FROM pv_part."+spec.name+"($1::regclass,$2::bigint)"
			for _, args := range [][]any{{nil, int64(-1)}, {"pv_part.heap", nil}, {nil, nil}} {
				pvBoth(t, p, b, c, pvMetadata(fixture.Select(q, spec.names, spec.types, [][]any{make([]any, len(spec.names))}, args...), b.PG))
				pvBoth(t, p, b, c, fixture.Select("SELECT pv_part."+spec.name+"($1::regclass,$2::bigint) AS result", []string{"result"}, []string{"RECORD"}, [][]any{{nil}}, args...))
			}
			for _, block := range []int64{-1, 4294967295, 9223372036854775807} {
				pvBoth(t, p, b, c, pvError(q, "22023", "invalid block number", "pv_part.heap", block))
			}
		}
		for _, spec := range []struct{ name string; names, types []string; set bool }{
			{"pg_visibility_map", pvMapRowsNames, pvMapRowsTypes, true},
			{"pg_visibility", pvPageRowsNames, pvPageRowsTypes, true},
			{"pg_visibility_map_summary", pvMapNames, pvSummaryTypes, false},
		} {
			var expected [][]any
			if !spec.set { expected = [][]any{{nil, nil}} }
			pvBoth(t, p, b, c, pvMetadata(fixture.Select("SELECT * FROM pv_part."+spec.name+"($1::regclass)", spec.names, spec.types, expected, nil), b.PG))
		}
		pvBoth(t, p, b, c, fixture.Select("SELECT pv_part.pg_visibility_map_summary(NULL::regclass) AS result", []string{"result"}, []string{"RECORD"}, [][]any{{nil}}))
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
		pvFreeze(t, p, b, c)
		pvOneFrozen(t, p, b, c)
		pvProbe(t, p, b, c, "pv_part.heap", 4294967294, false, false, false)
		pvOneFrozen(t, p, b, c)
	})
}

func TestReferencePgvisibilityDiagnostics(t *testing.T) {
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS pv_fdw CASCADE")
		for _, q := range []string{
			"CREATE UNLOGGED TABLE pv_part.unlogged(k integer)",
			"CREATE TEMP TABLE pv_owned_temp(k integer)",
			"CREATE MATERIALIZED VIEW pv_part.mv AS SELECT k FROM pv_part.heap",
			"CREATE TABLE pv_part.parent(k integer) PARTITION BY RANGE(k)",
			"CREATE TABLE pv_part.leaf PARTITION OF pv_part.parent DEFAULT",
			"CREATE INDEX heap_index ON pv_part.heap(k)",
			"CREATE SEQUENCE pv_part.seq",
			"CREATE VIEW pv_part.v AS SELECT k FROM pv_part.heap",
			"CREATE TYPE pv_part.composite AS(k integer)",
			"CREATE FOREIGN DATA WRAPPER pv_fdw",
			"CREATE SERVER pv_server FOREIGN DATA WRAPPER pv_fdw",
			"CREATE FOREIGN TABLE pv_part.foreign_heap(k integer) SERVER pv_server",
		} { pvRun(t, p, b, c, fixture.Exec(q, 0)) }
		for _, target := range []string{"pv_part.heap", "pv_part.unlogged", "pv_owned_temp", "pv_part.mv", "pv_part.leaf"} {
			pvEmpty(t, p, b, c, target)
			pvProbe(t, p, b, c, target, 0, false, false, false)
		}
		wrongKind := func(name, plural string) {
			t.Helper()
			for _, call := range []string{"pg_visibility_map($1::regclass)", "pg_visibility($1::regclass)", "pg_visibility_map_summary($1::regclass)", "pg_visibility_map($1::regclass,-1)", "pg_visibility($1::regclass,-1)"} {
				s := pvError("SELECT * FROM pv_part."+call, "42809", "relation \""+name+"\" is of wrong relation kind", "pv_part."+name)
				s.Diagnostic.Detail = "This operation is not supported for "+plural+"."
				pvBoth(t, p, b, c, s)
			}
		}
		for _, tc := range []struct{ name, plural string }{
			{"parent", "partitioned tables"}, {"heap_index", "indexes"},
			{"seq", "sequences"}, {"v", "views"}, {"foreign_heap", "foreign tables"}, {"composite", "composite types"},
		} {
			wrongKind(tc.name, tc.plural)
		}
		// Native descriptor/current-host units supplement partitioned-index rejection.
		// Genuine TOAST admission is PostgreSQL-only: native has no physical TOAST relation.
		if b.PG {
			pvRun(t, p, b, c, fixture.Exec("CREATE INDEX parent_index ON pv_part.parent(k)", 0))
			wrongKind("parent_index", "partitioned indexes")
			pvRun(t, p, b, c, fixture.Exec("CREATE TABLE pv_part.toasted(v text)", 0))
			var toast string
			if err := c.QueryRowContext(p.Ctx, "SELECT reltoastrelid::regclass::text FROM pg_class WHERE oid='pv_part.toasted'::regclass").Scan(&toast); err != nil { t.Fatal(err) }
			pvEmpty(t, p, b, c, toast)
			pvProbe(t, p, b, c, toast, 0, false, false, false)
		}
		pvRun(t, p, b, c, fixture.Exec("CREATE TABLE pv_part.doomed(k integer)", 0))
		dropped := pvOID(t, p, c, "pv_part.doomed")
		pvRun(t, p, b, c, fixture.Exec("DROP TABLE pv_part.doomed", 0))
		for _, oid := range []int64{0, 4294967295, dropped} {
			for _, call := range []string{"pg_visibility_map($1::regclass)", "pg_visibility($1::regclass)", "pg_visibility_map_summary($1::regclass)", "pg_visibility_map($1::regclass,-1)", "pg_visibility($1::regclass,-1)"} {
				pvBoth(t, p, b, c, pvError("SELECT * FROM pv_part."+call, "XX000", fmt.Sprintf("could not open relation with OID %d", oid), oid))
			}
		}
		for _, call := range []string{"pg_visibility_map($1::text::regclass)", "pg_visibility($1::text::regclass)", "pg_visibility_map_summary($1::text::regclass)"} {
			pvBoth(t, p, b, c, pvError("SELECT * FROM pv_part."+call, "42P01", "relation \"pv_part.absent\" does not exist", "pv_part.absent"))
		}
		for _, call:=range []string{"pg_visibility_map('pv_part.absent'::regclass)","pg_visibility('pv_part.absent'::regclass,0)","pg_visibility_map_summary('pv_part.absent'::regclass)"} {
			q:="SELECT * FROM pv_part."+call
			s:=pvError(q,"42P01","relation \"pv_part.absent\" does not exist")
			s.Diagnostic.Position=fixture.Position(strings.Index(q,"'pv_part.absent'")+1)
			pvRun(t,p,b,c,s)
			st,err:=c.PrepareContext(p.Ctx,q)
			if st!=nil {st.Close()}
			fixture.AssertDiagnostic(t,err,s.Diagnostic)
		}
		s := pvError("SELECT * FROM pv_part.pg_visibility_map($1::regclass,0)", "42602", "invalid name syntax", "pv_part..heap")
		s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
		pvBoth(t, p, b, c, s)
		// Input conversion fails at protocol Bind, with the real parameter
		// provenance; this is not an extension-specific argument validator.
		s = pvError("SELECT * FROM pv_part.pg_visibility_map('pv_part.heap',$1::bigint)", "22P02", "invalid input syntax for type bigint: \"bad\"", "bad")
		s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
		pvBoth(t, p, b, c, s)
	})
}

func TestReferencePgvisibilityForeignTemporary(t *testing.T) {
	for _, phase := range []string{"empty", "inserted_vm_absent", "vacuumed_vm_present"} {
		t.Run(phase, func(t *testing.T) {
			pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, reader *sql.Conn) {
				owner := pvConn(t, p, b); defer owner.Close()
				pvRun(t, p, b, owner, fixture.Exec("CREATE TEMP TABLE pv_foreign_temp(k integer)", 0))
				defer owner.ExecContext(p.Ctx, "DROP TABLE pv_foreign_temp")
				if phase != "empty" { pvRun(t, p, b, owner, fixture.Exec("INSERT INTO pv_foreign_temp VALUES(1)", 1)) }
				if phase == "vacuumed_vm_present" { pvRun(t, p, b, owner, fixture.Exec("VACUUM (FREEZE) pv_foreign_temp", 0)) }
				oid := pvOID(t, p, owner, "pv_foreign_temp")
				var namespace string
				if err:=owner.QueryRowContext(p.Ctx,"SELECT n.nspname FROM pg_namespace n WHERE n.oid=pg_my_temp_schema()").Scan(&namespace);err!=nil {t.Fatal(err)}
				if got:=pvOID(t,p,reader,namespace+".pv_foreign_temp");got!=oid {t.Fatal("foreign TEMP namespace resolved another identity",got,oid)}
				ownerVisible:=int64(0)
				if phase=="vacuumed_vm_present" {ownerVisible=1}
				pvBoth(t,p,b,owner,fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary($1::regclass)",pvMapNames,pvSummaryTypes,[][]any{{ownerVisible,ownerVisible}},oid))
				pvProbe(t,p,b,owner,oid,0,ownerVisible==1,ownerVisible==1,ownerVisible==1)
				// VM calls read buffers only for existing VM pages. Explicit pd
				// probes skip heap reads beyond EOF, but the page-reading SRF
				// initializes its read stream even for an empty foreign heap.
				// Isolate every cell and execution route: one diagnostic mismatch
				// must not prevent observation of later independent expectations.
				runCell := func(name string, s fixture.Step) {
					t.Run(name, func(t *testing.T) {
						t.Run("direct", func(t *testing.T) { pvRun(t, p, b, reader, s) })
						t.Run("prepared", func(t *testing.T) { pvRun(t, p, b, reader, fixture.Prepared(s)) })
					})
				}
				for _, fn := range []string{"pg_visibility_map", "pg_visibility"} {
					for _, block := range []int64{0, 4294967294} {
						q := "SELECT * FROM pv_part."+fn+"($1::regclass,$2::bigint)"
						name := fmt.Sprintf("%s/block_%d", fn, block)
						needsBuffer := block == 0 && (phase == "vacuumed_vm_present" || fn == "pg_visibility" && phase != "empty")
						if needsBuffer {
							runCell(name, pvError(q, "0A000", "cannot access temporary tables of other sessions", oid, block))
						} else if fn == "pg_visibility" {
							runCell(name, fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{false, false, false}}, oid, block))
						} else { runCell(name, fixture.Select(q, pvMapNames, pvMapTypes, [][]any{{false, false}}, oid, block)) }
					}
					runCell(fn+"/invalid_block", pvError("SELECT * FROM pv_part."+fn+"($1::regclass,-1)", "22023", "invalid block number", oid))
				}
				for _, fn := range []string{"pg_visibility_map", "pg_visibility", "pg_visibility_map_summary"} {
					q := "SELECT * FROM pv_part."+fn+"($1::regclass)"
					if phase == "vacuumed_vm_present" || fn == "pg_visibility" {
						runCell(fn+"/relation", pvError(q, "0A000", "cannot access temporary tables of other sessions", oid))
					} else if fn == "pg_visibility_map_summary" {
						runCell(fn+"/relation", fixture.Select(q, pvMapNames, pvSummaryTypes, [][]any{{int64(0), int64(0)}}, oid))
					} else {
						var expected [][]any
						if phase != "empty" { expected = [][]any{{int64(0), false, false}} }
						runCell(fn+"/relation", fixture.Select(q+" ORDER BY blkno", pvMapRowsNames, pvMapRowsTypes, expected, oid))
					}
				}
			})
		})
	}
}
