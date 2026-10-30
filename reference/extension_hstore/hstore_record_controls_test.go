package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hstoreRecordSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreRecordSetup(t, p, b, c)
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_record_stored(id integer,r hs_record_pair DEFAULT populate_record('(7,keep)'::hs_record_pair,'i=>42'::hstore),o hs_record_pair DEFAULT ('(7,keep)'::hs_record_pair #= 'n=>NULL'::hstore),h hstore DEFAULT hstore(NULL::hs_record_pair))", 0))
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_stored(id) VALUES (1),(2)", 2))
	hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_record_view AS SELECT id,hstore(r)::text AS made,populate_record(r,'n=>view'::hstore)::text AS named,(r #= 'i=>43'::hstore)::text AS operated FROM hs_record_stored", 0))
}

func hstoreRecordStoredVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	hstoreRecordBoth(t, p, b, c, fixture.Select("SELECT id,r::text AS r,o::text AS o,h::text AS h FROM hs_record_stored WHERE id<=2 ORDER BY id", []string{"id", "r", "o", "h"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "(42,keep)", "(7,)", `"i"=>NULL, "n"=>NULL`}, {int64(2), "(42,keep)", "(7,)", `"i"=>NULL, "n"=>NULL`}}))
	hstoreRecordBoth(t, p, b, c, fixture.Select("SELECT id,made,named,operated FROM hs_record_view WHERE id<=2 ORDER BY id", []string{"id", "made", "named", "operated"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), `"i"=>"42", "n"=>"keep"`, "(42,view)", "(43,keep)"}, {int64(2), `"i"=>"42", "n"=>"keep"`, "(42,view)", "(43,keep)"}}))
	hstoreRecordBoth(t, p, b, c, hstoreSelect(b, "SELECT h FROM hs_record_stored WHERE id<=2 ORDER BY id", []string{"h"}, [][]any{{`"i"=>NULL, "n"=>NULL`}, {`"i"=>NULL, "n"=>NULL`}}, []bool{true}))
	hstoreRecordBoth(t, p, b, c, hstoreRecordRaw(b, "SELECT r AS v FROM hs_record_stored WHERE id<=2 ORDER BY id", [][]any{{"(42,keep)"}, {"(42,keep)"}}, true))
}

func hstoreRecordFreshCalls(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	hstoreRecordBoth(t, p, b, c, hstoreSelect(b, "SELECT hstore($1::hs_record_pair) AS v", []string{"v"}, [][]any{{`"i"=>"9", "n"=>"new"`}}, nil, "(9,new)"))
	for _, operator := range []bool{false, true} {
		hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT "+hstoreRecordCall(operator, "$1::hs_record_pair", "$2::hstore")+"::text AS v", "(51,new)", "(9,new)", "i=>51"))
	}
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_stored(id) VALUES (3)", 1))
	hstoreRun(t, p, b, c, fixture.Select("SELECT r::text AS r,o::text AS o,h::text AS h FROM hs_record_stored WHERE id=3", []string{"r", "o", "h"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"(42,keep)", "(7,)", `"i"=>NULL, "n"=>NULL`}}))
}

func hstoreRecordRetained(t *testing.T, p *fixture.Pair, b fixture.Backend, rows *sql.Rows, columns []*sql.ColumnType) {
	t.Helper()
	after, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, after) {
		t.Fatal("retained composite metadata changed")
	}
	var got []any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []any{"(42,keep)", "(42,keep)"}) {
		t.Fatalf("retained records %#v", got)
	}
	rows.Close()
}

func TestReferenceHstoreRecordControls(t *testing.T) {
	for _, phase := range []string{"same", "fresh", "failed", "owner_constructor", "owner_replacement", "owner_operator"} {
		t.Run(phase, func(t *testing.T) {
			hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreRecordSeed(t, p, b, c)
				hstoreRecordStoredVerify(t, p, b, c)
				hstoreRecordIdentity(t, p, b, c, "hstore", "populate_record")
				if b.PG {
					return
				} // Public controls have no PostgreSQL equivalent.
				dump := hstoreDump(t, p, p.Native)
				rows, err := b.DB.QueryContext(p.Ctx, "SELECT r AS v FROM hs_record_stored ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				fixture.EnumReferenceMetadata(false, true)(t, rows)
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				rawQueries := []string{"SELECT hstore(r) AS v FROM hs_record_stored WHERE id=1", "SELECT populate_record(r,'i=>43'::hstore) AS v FROM hs_record_stored WHERE id=1"}
				var held []fixture.HeldStatement
				for _, q := range rawQueries {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
				}
				hstoreRun(t, p, b, held[0], hstoreSelect(b, rawQueries[0], []string{"v"}, [][]any{{`"i"=>"42", "n"=>"keep"`}}, nil))
				hstoreRun(t, p, b, held[1], hstoreRecordRaw(b, rawQueries[1], [][]any{{"(43,keep)"}}, false))
				textQuery := "SELECT populate_record($1::text::hs_record_pair,$2::text::hstore)::text AS v"
				stmt, err := c.PrepareContext(p.Ctx, textQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				textHeld := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: textQuery}
				hstoreRun(t, p, b, textHeld, hstoreRecordText(textQuery, "(42,before)", "(7,before)", "i=>42"))
				if phase == "same" {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
				} else if phase == "fresh" {
					destination := fixture.NewFixture(t, false, true)
					if _, err := destination.Native.ExecContext(p.Ctx, "CREATE TABLE hs_record_displaced(v integer); CREATE EXTENSION hstore; CREATE TYPE hs_record_displaced_type AS (v text)"); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
					hstoreRecordStoredVerify(t, p, target, destination.Memory)
					hstoreRecordIdentity(t, p, target, destination.Memory, "hstore", "populate_record")
					hstoreRecordFreshCalls(t, p, target, destination.Memory)
				} else if phase == "failed" {
					bad := string(dump) + "\nINSERT INTO hs_record_stored(id,r) VALUES (4,populate_record(NULL::hs_record_pair,'i=>oops'::hstore));\n"
					want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "oops"`)
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
					if !bytes.Equal(dump, hstoreDump(t, p, p.Native)) {
						t.Fatal("failed Restore changed incumbent dump")
					}
				} else {
					hstoreRecordRejectOwner(t, p, dump, phase)
				}
				hstoreRecordStoredVerify(t, p, b, c)
				hstoreRecordIdentity(t, p, b, c, "hstore", "populate_record")
				hstoreRecordRetained(t, p, b, rows, columns)
				if phase == "same" {
					for i, q := range rawQueries {
						hstoreError(t, p, b, held[i], q, false, true, false, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
					}
					hstoreRecordFreshCalls(t, p, b, c)
					hstoreRecordReplayDump(t, p, dump, false)
				} else {
					hstoreRun(t, p, b, held[0], hstoreSelect(b, rawQueries[0], []string{"v"}, [][]any{{`"i"=>"42", "n"=>"keep"`}}, nil))
					hstoreRun(t, p, b, held[1], hstoreRecordRaw(b, rawQueries[1], [][]any{{"(43,keep)"}}, false))
				}
				hstoreRun(t, p, b, textHeld, hstoreRecordText(textQuery, "(51,after)", "(9,after)", "i=>51"))
			})
		})
	}
}
