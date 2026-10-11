package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSmokeBetweenSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, ctx := backend.DB, p.Ctx
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, db, backend.Recorder, s) }
			selectStep := func(q string, types []string, want [][]any, args ...any) fixture.Step {
				names := make([]string, len(types))
				for i := range names {
					names[i] = "?column?"
					if types[i] == "INT4" {
						names[i] = "id"
					}
				}
				s := fixture.Select(q, names, types, want, args...)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					meta, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.ScalarAssertMetadata(t, backend.PG, meta)
				}
				return s
			}
			run(fixture.Exec("CREATE TABLE t(id INTEGER,v INTEGER)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,0),(2,1),(3,2),(4,3),(5,4),(6,NULL)", 6))
			run(selectStep("SELECT v BETWEEN 1 AND 3,v NOT BETWEEN 1 AND 3,v BETWEEN SYMMETRIC 3 AND 1,v NOT BETWEEN SYMMETRIC 3 AND 1 FROM t ORDER BY id", []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}, {true, false, true, false}, {true, false, true, false}, {true, false, true, false}, {false, true, false, true}, {nil, nil, nil, nil}}))
			for _, tc := range []struct {
				expr string
				want [][]any
			}{
				{"v BETWEEN 3 AND 1", [][]any{{false}, {false}, {false}, {false}, {false}, {nil}}},
				{"v BETWEEN 2 AND 2", [][]any{{false}, {false}, {true}, {false}, {false}, {nil}}},
			} {
				run(selectStep("SELECT "+tc.expr+" FROM t ORDER BY id", []string{"BOOL"}, tc.want))
			}
			run(selectStep("SELECT 5 BETWEEN NULL AND 3,5 NOT BETWEEN NULL AND 3,5 BETWEEN SYMMETRIC NULL AND 3,TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE FROM t WHERE id=1", []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, nil, true}}))
			run(selectStep("SELECT TRUE BETWEEN (1<2) AND (2<3),TRUE BETWEEN 1<2 AND (2<3),2 BETWEEN 1 AND 3 = TRUE FROM t WHERE id=1", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			run(selectStep("SELECT id FROM t WHERE v BETWEEN (1+0) AND (2+1) AND TRUE BETWEEN (FALSE OR TRUE) AND TRUE ORDER BY id", []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}}))
			const query = "SELECT $1 BETWEEN 1 AND 3 FROM t WHERE id=1"
			stmt, err := db.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: query}
			for _, arg := range []any{2, nil, 2} {
				var want any = true
				if arg == nil {
					want = nil
				}
				fixture.RunStep(t, ctx, held, backend.Recorder, selectStep(query, []string{"BOOL"}, [][]any{{want}}, arg))
			}
			stmt.Close()
			for _, tc := range []struct{ q, message, position string }{
				{"SELECT v NOT BETWEEN 1 AND TRUE FROM t", "operator does not exist: integer > boolean", "10"},
				{"SELECT TRUE BETWEEN 1<2 AND 2<3 FROM t WHERE id=1", "operator does not exist: boolean <= integer", "13"},
			} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec("CREATE TABLE temporal_bounds(d DATE,ts TIMESTAMP)", 0))
			run(fixture.Exec("INSERT INTO temporal_bounds VALUES('2000-01-01','2000-01-03')", 1))
			for _, form := range []string{"BETWEEN", "NOT BETWEEN", "BETWEEN SYMMETRIC", "NOT BETWEEN SYMMETRIC"} {
				q := "SELECT '2000-01-02' " + form + " d AND ts FROM temporal_bounds"
				s := selectStep(q, []string{"BOOL"}, [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}})
				run(s)
				stmt, err := db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}
				for range 2 {
					fixture.RunStep(t, ctx, held, backend.Recorder, s)
				}
				stmt.Close()
			}
			run(fixture.Exec("CREATE TABLE defaults(b BOOL DEFAULT (2 BETWEEN SYMMETRIC 3 AND 1),c BOOL DEFAULT (TRUE BETWEEN (1<2) AND (2<3)),s BOOL DEFAULT (TRUE BETWEEN TRUE AND (2 BETWEEN 1 AND 3)),n BOOL DEFAULT ((TRUE OR FALSE) NOT BETWEEN SYMMETRIC FALSE AND TRUE))", 0))
			run(fixture.Exec("INSERT INTO defaults DEFAULT VALUES", 1))
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("INSERT INTO defaults DEFAULT VALUES", 1))
			run(selectStep("SELECT b BETWEEN FALSE AND TRUE,c BETWEEN FALSE AND TRUE,s BETWEEN FALSE AND TRUE,n BETWEEN TRUE AND TRUE FROM defaults", []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false}, {true, true, true, false}}))
		})
	}
}
