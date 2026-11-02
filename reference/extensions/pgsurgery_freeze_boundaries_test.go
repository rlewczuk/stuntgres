package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryFreezeArrays(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'kept') RETURNING k,v,ctid,xmin,xmax")
		for _, args := range [][]any{{nil, "{NULL}"}, {int64(0), nil}, {nil, nil}} {
			freezeBoth(t, p, b, c, fixture.Select(freezeSQL, []string{"repaired"}, []string{"VOID"}, [][]any{{nil}}, args...))
		}
		freezeBoth(t, p, b, c, freezeVoid(freezeSQL, "fs_part.heap", "{}"))
		freezeExpect(t, p, b, c, a, z)
		for _, tc := range []struct{ input, code, message string }{
			{"{NULL}", "22004", "array must not contain nulls"},
			{"{{\"(0,1)\"},{\"(0,2)\"}}", "22000", "argument must be empty or one-dimensional array"},
			{"{{NULL},{\"(0,1)\"}}", "22004", "array must not contain nulls"},
		} {
			freezeBoth(t, p, b, c, freezeError(freezeSQL, tc.code, tc.message, "fs_part.heap", tc.input))
			freezeExpect(t, p, b, c, a, z)
		}
		// Lower bounds have no bearing on payload positions.
		freezeBoth(t, p, b, c, freezeVoid(freezeSQL, "fs_part.heap", "[0:0]="+freezeTIDs(a.tid)))
		a = freezeRepaired(a)
		freezeExpect(t, p, b, c, a, z)
		st, err := c.PrepareContext(p.Ctx, freezeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: freezeSQL}
		input := freezeTIDs(z.tid, a.tid, z.tid)
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, freezeVoid(freezeSQL, "fs_part.heap", input))
		}
		z = freezeRepaired(z)
		freezeExpect(t, p, b, c, a, z)
		// Exactly 65,536 elements crosses the released helper's uint16 boundary.
		largeTarget := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(3,'large') RETURNING k,v,ctid,xmin,xmax")
		large := "{" + strings.Repeat(fmt.Sprintf("%q,", largeTarget.tid), 65535) + fmt.Sprintf("%q}", largeTarget.tid)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, freezeVoid(freezeSQL, "fs_part.heap", large))
		freezeExpect(t, p, b, c, a, z, freezeRepaired(largeTarget))
	})
}

func TestReferencePgsurgeryFreezeDiagnostics(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS fs_fdw CASCADE")
		for _, q := range []string{
			"CREATE VIEW fs_part.v AS SELECT k FROM fs_part.heap", "CREATE SEQUENCE fs_part.seq",
			"CREATE INDEX fs_index ON fs_part.heap(k)", "CREATE TYPE fs_part.composite AS(k integer)",
			"CREATE TABLE fs_part.parent(k integer) PARTITION BY RANGE(k)",
			"CREATE UNIQUE INDEX parent_index ON fs_part.parent(k)",
			"CREATE FOREIGN DATA WRAPPER fs_fdw", "CREATE SERVER fs_server FOREIGN DATA WRAPPER fs_fdw",
			"CREATE FOREIGN TABLE fs_part.ft(k integer) SERVER fs_server",
			"CREATE MATERIALIZED VIEW fs_part.mv AS SELECT k FROM fs_part.heap",
			"CREATE UNLOGGED TABLE fs_part.unlogged(k integer)", "CREATE TEMP TABLE fs_temp(k integer)",
		} {
			freezeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ name, kind string }{{"v", "views"}, {"seq", "sequences"}, {"fs_index", "indexes"}, {"composite", "composite types"}, {"parent", "partitioned tables"}, {"parent_index", "partitioned indexes"}, {"ft", "foreign tables"}} {
			t.Run(tc.name, func(t *testing.T) {
				s := freezeError(freezeSQL, "42809", "cannot operate on relation \""+tc.name+"\"", "fs_part."+tc.name, "{}")
				s.Diagnostic.Detail = "This operation is not supported for " + tc.kind + "."
				freezeBoth(t, p, b, c, s)
			})
		}
		for _, name := range []string{"fs_part.heap", "fs_part.mv", "fs_part.unlogged", "fs_temp"} {
			freezeBoth(t, p, b, c, freezeVoid(freezeSQL, name, "{}"))
		}
		for _, tc := range []struct {
			name, q, code, message string
			args                   []any
		}{
			{"missing_oid", freezeSQL, "XX000", "could not open relation with OID 0", []any{int64(0), "{}"}},
			{"missing_name", "SELECT fs_part.heap_force_freeze($1::text::regclass,'{}'::tid[]) AS repaired", "42P01", "relation \"fs_part.absent\" does not exist", []any{"fs_part.absent"}},
			{"bad_tid", "SELECT fs_part.heap_force_freeze('fs_part.heap'::regclass,ARRAY[$1::text::tid]) AS repaired", "22P02", "invalid input syntax for type tid: \"bad\"", []any{"bad"}},
		} {
			t.Run(tc.name, func(t *testing.T) { freezeBoth(t, p, b, c, freezeError(tc.q, tc.code, tc.message, tc.args...)) })
		}
		t.Run("wrong_type", func(t *testing.T) {
			q := "SELECT fs_part.heap_force_freeze('fs_part.heap'::regclass,1)"
			s := freezeError(q, "42883", "function fs_part.heap_force_freeze(regclass, integer) does not exist")
			s.Diagnostic.Position = "8"
			s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			freezeBoth(t, p, b, c, s)
		})
	})
}

func TestReferencePgsurgeryFreezeNotices(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		var block, offset int
		if _, err := fmt.Sscanf(a.tid, "(%d,%d)", &block, &offset); err != nil {
			t.Fatal(err)
		}
		notice := func(code, message string) fixture.Diagnostic {
			return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: code, Message: message}
		}
		want := []fixture.Diagnostic{
			notice("00000", fmt.Sprintf("skipping tid (%d, 0) for relation \"heap\" because the item number is out of range", block)),
			notice("00000", fmt.Sprintf("skipping tid (%d, 0) for relation \"heap\" because the item number is out of range", block)),
			notice("00000", fmt.Sprintf("skipping tid (%d, 65535) for relation \"heap\" because the item number is out of range", block)),
			notice("22023", "skipping block 4294967294 for relation \"heap\" because the block number is out of range"),
		}
		for _, prepared := range []bool{false, true} {
			notices = nil
			s := freezeVoid(freezeSQL, "fs_part.heap", freezeTIDs("(4294967294,1)", fmt.Sprintf("(%d,65535)", block), a.tid, fmt.Sprintf("(%d,0)", block), "(4294967294,2)", fmt.Sprintf("(%d,0)", block)))
			if prepared {
				s = fixture.Prepared(s)
			}
			freezeRun(t, p, b, c, s)
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices\ngot  %#v\nwant %#v", notices, want)
			}
			freezeExpect(t, p, b, c, freezeRepaired(a))
		}
		notices = nil
		freezeApply(t, p, b, c, a.tid)
		if len(notices) != 0 {
			t.Fatalf("ordinary surgery invented warning: %#v", notices)
		}
	})
}
