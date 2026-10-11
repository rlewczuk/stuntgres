package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Preserve the distinct root caller and smoke source populations: their counts,
// bags, held metadata and sequence effects are not interchangeable.
func TestReferenceBooleanAggregateMigrationLifecycle(t *testing.T) {
	for _, population := range []string{"caller", "smoke"} {
		t.Run(population, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(k TEXT,b BOOL)", 0))
					run(fixture.Exec("CREATE TABLE m(b BOOL)", 0))
					run(fixture.Exec("INSERT INTO m VALUES(TRUE),(FALSE),(NULL)", 3))
					values, count := "('T',TRUE),('F',FALSE),('M',TRUE),('M',FALSE),('N',NULL),(NULL,TRUE)", int64(6)
					if population == "smoke" {
						values, count = "('T',TRUE),('T',TRUE),('T',NULL),('F',FALSE),('F',FALSE),('F',NULL),('M',TRUE),('M',FALSE),('M',NULL),('N',NULL),('N',NULL)", 11
					}
					run(fixture.Exec("INSERT INTO t VALUES"+values, count))
					run(fixture.Exec("CREATE TABLE empty(k TEXT,b BOOL)", 0))
					viewSource := "t"
					if population == "smoke" {
						viewSource = "m"
					}
					run(fixture.Exec("CREATE VIEW aggregated(a,o,e,u) AS SELECT bool_and(b),bool_or(b),every(b),every('true') FROM "+viewSource, 0))
					if population == "smoke" {
						run(fixture.Exec("CREATE TABLE out(k TEXT,b BOOL)", 0))
						for _, s := range []fixture.Step{fixture.Exec("INSERT INTO out SELECT k,bool_and(b) FROM t GROUP BY k", 4), fixture.Exec("INSERT INTO out(b) SELECT every(b) FROM empty", 1), fixture.Exec("INSERT INTO out(b) SELECT bool_or(b) FROM empty GROUP BY k", 0), fixture.Exec("INSERT INTO out(b) SELECT bool_or(b) FROM m", 1)} {
							run(s)
						}
						run(fixture.Exec("CREATE TABLE d(v INT)", 0))
						run(fixture.Exec("INSERT INTO d VALUES(1),(0)", 2))
						for _, q := range []string{"SELECT bool_and(10/v=0) FROM d", "SELECT bool_or(10/v>0) FROM d", "INSERT INTO out(b) SELECT bool_and(10/v=0) FROM d"} {
							for _, prepared := range []bool{false, true} {
								s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
								s.Prepared = prepared
								run(s)
							}
						}
						s := fixture.Select("SELECT k,b FROM out", []string{"k", "b"}, []string{"TEXT", "BOOL"}, [][]any{{"T", true}, {"F", false}, {"M", false}, {"N", nil}, {nil, nil}, {nil, true}})
						s.Bag = true
						run(s)
					}
					q := "SELECT bool_and(b),bool_or(b) FROM t WHERE k='T'"
					columns, types, wantOld, wantNew := []string{"bool_and", "bool_or"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, [][]any{{false, true}}
					if population == "smoke" {
						q = "SELECT bool_and(b) FROM t WHERE k='T'"
						columns, types, wantOld, wantNew = columns[:1], types[:1], [][]any{{true}}, [][]any{{false}}
					}
					// The held cursor reserves its pool connection. Mutations and
					// sequence effects stay on the independent pinned session.
					stmt, err := backend.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					backend.Recorder.Mu.Lock()
					heldRecord := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
					backend.Recorder.Mu.Unlock()
					run(fixture.Exec("INSERT INTO t VALUES('T',FALSE)", 1))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, columns, types, wantNew))
					check := func() {
						for _, prepared := range []bool{false, true} {
							s := fixture.Select("SELECT a,o,e,u FROM aggregated", []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}})
							s.Prepared = prepared
							run(s)
						}
					}
					check()
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					}
					if got := fixture.AnyValueConsume(t, held, backend.Recorder, q, columns, types, heldRecord); !reflect.DeepEqual(got, wantOld) {
						t.Fatalf("held %#v want %#v", got, wantOld)
					}
					check()
					if population == "caller" {
						run(fixture.Exec("CREATE SEQUENCE s", 0))
						run(fixture.Select("SELECT bool_and(nextval('s')>0),every(nextval('s')>0),bool_or(nextval('s')>0) FROM t", []string{"bool_and", "every", "bool_or"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
						run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(21)}}))
					}
				})
			}
		})
	}
}
