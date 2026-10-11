package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBooleanAggregateCallers(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(k TEXT,b BOOL)", 0), fixture.Exec("INSERT INTO t VALUES('T',TRUE),('F',FALSE),('M',TRUE),('M',FALSE),('N',NULL),(NULL,TRUE)", 6), fixture.Exec("CREATE TABLE empty(k TEXT,b BOOL)", 0), fixture.Exec("CREATE TABLE d(v INT)", 0), fixture.Exec("INSERT INTO d VALUES(1),(0)", 2), fixture.Exec("CREATE TABLE out(b BOOL)", 0), fixture.Exec("INSERT INTO out VALUES(TRUE)", 1), fixture.Exec("CREATE VIEW aggregated(a,o,e,u) AS SELECT bool_and(b),bool_or(b),every(b),every('true') FROM t", 0)} {
				apply(t, s)
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT k FROM t GROUP BY k HAVING bool_or(b)", []string{"k"}, []string{"TEXT"}, [][]any{{"T"}, {"M"}, {nil}}),
				fixture.Select("SELECT k FROM t GROUP BY k HAVING every(b)", []string{"k"}, []string{"TEXT"}, [][]any{{"T"}, {nil}}),
				fixture.Select("SELECT k,bool_and(b) FROM t GROUP BY k ORDER BY bool_or(b)", []string{"k", "bool_and"}, []string{"TEXT", "BOOL"}, [][]any{{"T", true}, {"F", false}, {"M", false}, {"N", nil}, {nil, true}}),
				fixture.Select("SELECT COALESCE(bool_and(b),FALSE) AS answer FROM empty", []string{"answer"}, []string{"BOOL"}, [][]any{{false}}),
				fixture.Select("SELECT pg_catalog.bool_and(b),\"bool_or\"(b),every(b IS NULL) FROM t", []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false}}),
				fixture.Select("SELECT a,o,e,u FROM aggregated", []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}}),
			} {
				s.Bag = true
				apply(t, s)
				apply(t, fixture.Prepared(s))
			}
			for _, q := range []string{"SELECT bool_and(10/v=0) FROM d", "SELECT bool_or(10/v>0) FROM d", "INSERT INTO out SELECT bool_and(10/v=0) FROM d", "INSERT INTO out SELECT bool_or(10/v>0) FROM d"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.ErrorStage = "execute"
				apply(t, s)
				apply(t, fixture.Prepared(s))
				s.SelectRows = q[:6] == "SELECT"
				apply(t, s)
				apply(t, fixture.Prepared(s))
			}
			apply(t, fixture.Select("SELECT b FROM out", []string{"b"}, []string{"BOOL"}, [][]any{{true}}))
			for _, prepared := range []bool{false, true} {
				for _, s := range []fixture.Step{fixture.Exec("INSERT INTO out SELECT bool_and(b) FROM t", 1), fixture.Exec("INSERT INTO out SELECT bool_and(b) FROM t GROUP BY k", 5), fixture.Exec("INSERT INTO out SELECT every(b) FROM empty", 1), fixture.Exec("INSERT INTO out SELECT bool_or(b) FROM empty GROUP BY k", 0)} {
					s.Prepared = prepared
					apply(t, s)
				}
			}
			s := fixture.Select("SELECT b FROM out", []string{"b"}, []string{"BOOL"}, [][]any{{true}, {false}, {true}, {false}, {false}, {nil}, {true}, {nil}, {false}, {true}, {false}, {false}, {nil}, {true}, {nil}})
			s.Bag = true
			apply(t, s)
			const heldQuery = "SELECT bool_and(b),bool_or(b) FROM t WHERE k='T'"
			var held *sql.Stmt
			var err error
			if nonContext {
				held, err = p.Native.Prepare(heldQuery)
			} else {
				held, err = p.Native.PrepareContext(p.Ctx, heldQuery)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var old *sql.Rows
			if nonContext {
				old, err = held.Query()
			} else {
				old, err = held.QueryContext(p.Ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			apply(t, fixture.Exec("INSERT INTO t VALUES('T',FALSE)", 1))
			// Reuse the SAME native held statement after mutation.
			var rows *sql.Rows
			if nonContext {
				rows, err = held.Query()
			} else {
				rows, err = held.QueryContext(p.Ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var a, o bool
			if err := rows.Scan(&a, &o); err != nil || a || !o {
				t.Fatal(a, o, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
			view := fixture.Select("SELECT a,o,e,u FROM aggregated", []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}})
			apply(t, view)
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
			columns, err := old.Columns()
			if err != nil || len(columns) != 2 || columns[0] != "bool_and" || columns[1] != "bool_or" {
				t.Fatal(columns, err)
			}
			metadata, err := old.ColumnTypes()
			if err != nil || len(metadata) != 2 || metadata[0].DatabaseTypeName() != "BOOL" || metadata[1].DatabaseTypeName() != "BOOL" {
				t.Fatal(metadata, err)
			}
			if !old.Next() {
				t.Fatal(old.Err())
			}
			if err := old.Scan(&a, &o); err != nil || !a || !o {
				t.Fatal("retained aggregate", a, o, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal(old.Err())
			}
			old.Close()
			run(t, p.Ctx, p.Memory, p.MemoryRecord, view)
			apply(t, fixture.Exec("CREATE SEQUENCE s", 0))
			apply(t, fixture.Select("SELECT bool_and(nextval('s')>0),every(nextval('s')>0),bool_or(nextval('s')>0) FROM t", []string{"bool_and", "every", "bool_or"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			apply(t, fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(21)}}))
		})
	}
}

func TestReferenceBooleanAggregateCapabilityBoundaries(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { run(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE m(b BOOL)", 0))
					run(fixture.Exec("INSERT INTO m VALUES(TRUE),(FALSE),(NULL)", 3))
					for _, tc := range []struct {
						q, name string
						rows    [][]any
					}{
						{"SELECT bool_and(DISTINCT b) FROM m", "bool_and", [][]any{{false}}},
						{"SELECT bool_or(DISTINCT b) FROM m", "bool_or", [][]any{{true}}},
						{"SELECT every(DISTINCT b) FROM m", "every", [][]any{{false}}},
						{"SELECT bool_and(b) FILTER (WHERE FALSE) FROM m", "bool_and", [][]any{{nil}}},
						{"SELECT bool_and(b) FILTER (WHERE NULL) FROM m", "bool_and", [][]any{{nil}}},
						{"SELECT bool_and(b) FILTER (WHERE b) FROM m", "bool_and", [][]any{{true}}},
						{"SELECT bool_and(b) FILTER (WHERE NOT b) FROM m", "bool_and", [][]any{{false}}},
						{"SELECT bool_and(b) FILTER (WHERE b IS NULL) FROM m", "bool_and", [][]any{{nil}}},
						{"SELECT every(ALL b) FROM m", "every", [][]any{{false}}},
						{"SELECT bool_or(b ORDER BY b) FROM m", "bool_or", [][]any{{true}}},
						{"SELECT bool_and(TRUE)", "bool_and", [][]any{{true}}},
						{"SELECT bool_or(b) OVER () FROM m", "bool_or", [][]any{{true}, {true}, {true}}},
					} {
						for _, prepared := range []bool{false, true} {
							s := fixture.Select(tc.q, []string{tc.name}, []string{"BOOL"}, tc.rows)
							if !backend.PG && tc.q == "SELECT bool_or(b) OVER () FROM m" {
								s = fixture.Error(tc.q, "0A000")
								s.SelectRows = true
							}
							s.Prepared, s.Bag = prepared, true
							run(s)
						}
					}
				})
			}
		})
	}
}
