package reference_test

import (
	"context"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryAggregateSmokeMigration(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3))
			const q = "SELECT count(*) FROM t"
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
			record := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
			backend.Recorder.Mu.Unlock()
			run(fixture.Exec("INSERT INTO t VALUES('b',3)", 1))
			if got := fixture.AnyValueConsume(t, held, backend.Recorder, q, []string{"count"}, []string{"INT8"}, record); !reflect.DeepEqual(got, [][]any{{int64(3)}}) {
				t.Fatal(got)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
			run(fixture.Exec("CREATE TABLE out(k TEXT,n BIGINT)", 0))
			run(fixture.Exec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k", 3))
			want := [][]any{{"a", int64(2)}, {nil, int64(1)}, {"b", int64(1)}}
			s := fixture.Select("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, want)
			s.Bag = true
			run(s)
			for _, replacement := range []string{"reset", "restore"} {
				const grouped = "SELECT k,n FROM out GROUP BY k,n"
				rows, err := backend.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), grouped)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if !backend.PG {
					if replacement == "reset" {
						err = stuntgres.Reset(p.Ctx, p.Native)
					} else {
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(v INT)"))
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				got := fixture.AnyValueConsume(t, rows, backend.Recorder, grouped, []string{"k", "n"}, []string{"TEXT", "INT8"})
				if _, ok := fixture.CompareRows(got, want, true); !ok {
					t.Fatalf("held grouped %#v", got)
				}
				if !backend.PG && replacement == "reset" {
					run(fixture.Exec("CREATE TABLE out(k TEXT,n BIGINT)", 0))
					run(fixture.Exec("INSERT INTO out VALUES('a',2),(NULL,1),('b',1)", 3))
				}
			}
		})
	}
}

func TestReferenceCountExpressionSmokeMigration(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", 3), fixture.Exec("CREATE TABLE empty(k TEXT,v INT)", 0), fixture.Exec("CREATE TABLE out(k TEXT,n BIGINT)", 0)} {
				run(s)
			}
			const q = "SELECT count(v) FROM t"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func(n int64) {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{n}}))
			}
			run(fixture.Exec("INSERT INTO t VALUES('b',NULL)", 1))
			check(2)
			run(fixture.Exec("INSERT INTO t VALUES('b',4)", 1))
			check(3)
			for _, s := range []fixture.Step{fixture.Exec("INSERT INTO out SELECT k,count(v) FROM t GROUP BY k", 3), fixture.Exec("INSERT INTO out(n) SELECT count(v) FROM empty", 1), fixture.Exec("INSERT INTO out(n) SELECT count(v) FROM empty GROUP BY k", 0), fixture.Exec("INSERT INTO out(n) SELECT count(v) FROM t", 1)} {
				run(s)
			}
			s := fixture.Select("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}, {"b", int64(1)}, {nil, int64(0)}, {nil, int64(3)}})
			s.Bag = true
			run(s)
		})
	}
}
