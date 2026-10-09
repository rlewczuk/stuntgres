package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"testing"
)

// Preserve the distinct root caller and smoke source populations: their counts,
// bags, held metadata and sequence effects are not interchangeable.
func TestReferenceBooleanAggregateMigrationLifecycle(t *testing.T) {
	for _, population := range []string{"caller", "smoke"} {
		t.Run(population, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					conn, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(k TEXT,b BOOL)", 0))
					run(referenceExec("CREATE TABLE m(b BOOL)", 0))
					run(referenceExec("INSERT INTO m VALUES(TRUE),(FALSE),(NULL)", 3))
					values, count := "('T',TRUE),('F',FALSE),('M',TRUE),('M',FALSE),('N',NULL),(NULL,TRUE)", int64(6)
					if population == "smoke" {
						values, count = "('T',TRUE),('T',TRUE),('T',NULL),('F',FALSE),('F',FALSE),('F',NULL),('M',TRUE),('M',FALSE),('M',NULL),('N',NULL),('N',NULL)", 11
					}
					run(referenceExec("INSERT INTO t VALUES"+values, count))
					run(referenceExec("CREATE TABLE empty(k TEXT,b BOOL)", 0))
					viewSource := "t"
					if population == "smoke" {
						viewSource = "m"
					}
					run(referenceExec("CREATE VIEW aggregated(a,o,e,u) AS SELECT bool_and(b),bool_or(b),every(b),every('true') FROM "+viewSource, 0))
					if population == "smoke" {
						run(referenceExec("CREATE TABLE out(k TEXT,b BOOL)", 0))
						for _, s := range []referenceStep{referenceExec("INSERT INTO out SELECT k,bool_and(b) FROM t GROUP BY k", 4), referenceExec("INSERT INTO out(b) SELECT every(b) FROM empty", 1), referenceExec("INSERT INTO out(b) SELECT bool_or(b) FROM empty GROUP BY k", 0), referenceExec("INSERT INTO out(b) SELECT bool_or(b) FROM m", 1)} {
							run(s)
						}
						run(referenceExec("CREATE TABLE d(v INT)", 0))
						run(referenceExec("INSERT INTO d VALUES(1),(0)", 2))
						for _, q := range []string{"SELECT bool_and(10/v=0) FROM d", "SELECT bool_or(10/v>0) FROM d", "INSERT INTO out(b) SELECT bool_and(10/v=0) FROM d"} {
							for _, prepared := range []bool{false, true} {
								s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
								s.prepared = prepared
								run(s)
							}
						}
						s := referenceSelect("SELECT k,b FROM out", []string{"k", "b"}, []string{"TEXT", "BOOL"}, [][]any{{"T", true}, {"F", false}, {"M", false}, {"N", nil}, {nil, nil}, {nil, true}})
						s.bag = true
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
					stmt, err := backend.db.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					backend.recorder.mu.Lock()
					heldRecord := backend.recorder.operations[len(backend.recorder.operations)-1]
					backend.recorder.mu.Unlock()
					run(referenceExec("INSERT INTO t VALUES('T',FALSE)", 1))
					referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, columns, types, wantNew))
					check := func() {
						for _, prepared := range []bool{false, true} {
							s := referenceSelect("SELECT a,o,e,u FROM aggregated", []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}})
							s.prepared = prepared
							run(s)
						}
					}
					check()
					if !backend.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					}
					if got := anyValueConsume(t, held, backend.recorder, q, columns, types, heldRecord); !reflect.DeepEqual(got, wantOld) {
						t.Fatalf("held %#v want %#v", got, wantOld)
					}
					check()
					if population == "caller" {
						run(referenceExec("CREATE SEQUENCE s", 0))
						run(referenceSelect("SELECT bool_and(nextval('s')>0),every(nextval('s')>0),bool_or(nextval('s')>0) FROM t", []string{"bool_and", "every", "bool_or"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
						run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(21)}}))
					}
				})
			}
		})
	}
}
