package reference_test

import (
	"context"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceQueryAggregateSmokeMigration(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(k TEXT,v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3))
			const q = "SELECT count(*) FROM t"
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
			record := backend.recorder.operations[len(backend.recorder.operations)-1]
			backend.recorder.mu.Unlock()
			run(referenceExec("INSERT INTO t VALUES('b',3)", 1))
			if got := anyValueConsume(t, held, backend.recorder, q, []string{"count"}, []string{"INT8"}, record); !reflect.DeepEqual(got, [][]any{{int64(3)}}) {
				t.Fatal(got)
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
			run(referenceExec("CREATE TABLE out(k TEXT,n BIGINT)", 0))
			run(referenceExec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k", 3))
			want := [][]any{{"a", int64(2)}, {nil, int64(1)}, {"b", int64(1)}}
			s := referenceSelect("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, want)
			s.bag = true
			run(s)
			for _, replacement := range []string{"reset", "restore"} {
				const grouped = "SELECT k,n FROM out GROUP BY k,n"
				rows, err := backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), grouped)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if !backend.pg {
					if replacement == "reset" {
						err = stuntgres.Reset(p.ctx, p.native)
					} else {
						err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(v INT)"))
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				got := anyValueConsume(t, rows, backend.recorder, grouped, []string{"k", "n"}, []string{"TEXT", "INT8"})
				if _, ok := referenceCompareRows(got, want, true); !ok {
					t.Fatalf("held grouped %#v", got)
				}
				if !backend.pg && replacement == "reset" {
					run(referenceExec("CREATE TABLE out(k TEXT,n BIGINT)", 0))
					run(referenceExec("INSERT INTO out VALUES('a',2),(NULL,1),('b',1)", 3))
				}
			}
		})
	}
}

func TestReferenceCountExpressionSmokeMigration(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(k TEXT,v INT)", 0), referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", 3), referenceExec("CREATE TABLE empty(k TEXT,v INT)", 0), referenceExec("CREATE TABLE out(k TEXT,n BIGINT)", 0)} {
				run(s)
			}
			const q = "SELECT count(v) FROM t"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func(n int64) {
				referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{n}}))
			}
			run(referenceExec("INSERT INTO t VALUES('b',NULL)", 1))
			check(2)
			run(referenceExec("INSERT INTO t VALUES('b',4)", 1))
			check(3)
			for _, s := range []referenceStep{referenceExec("INSERT INTO out SELECT k,count(v) FROM t GROUP BY k", 3), referenceExec("INSERT INTO out(n) SELECT count(v) FROM empty", 1), referenceExec("INSERT INTO out(n) SELECT count(v) FROM empty GROUP BY k", 0), referenceExec("INSERT INTO out(n) SELECT count(v) FROM t", 1)} {
				run(s)
			}
			s := referenceSelect("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}, {"b", int64(1)}, {nil, int64(0)}, {nil, int64(3)}})
			s.bag = true
			run(s)
		})
	}
}
