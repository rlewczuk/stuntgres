package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateForms(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			fixture.QueryTableCreateSource(run)
			for i, q := range []string{
				"CREATE TABLE qc_form0 AS SELECT i,s FROM qc_source",
				"CREATE TABLE qc_form1 AS SELECT i,s FROM qc_source WITH DATA",
				"SELECT i,s INTO qc_form2 FROM qc_source",
			} {
				s := fixture.Exec(q, 3)
				// Actual plain DB consumer; the fresh Context route has its own target.
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				run(fixture.QueryTableCreateBag(fmt.Sprintf("qc_form%d", i)))
			}
			for form := range 2 {
				contextName := fmt.Sprintf("qc_context%d", form)
				plainName := fmt.Sprintf("qc_plain_prepared%d", form)
				contextQuery := "CREATE TABLE " + contextName + " AS SELECT i,s FROM qc_source"
				plainQuery := "CREATE TABLE " + plainName + " AS SELECT i,s FROM qc_source"
				if form == 1 {
					contextQuery = "SELECT i,s INTO " + contextName + " FROM qc_source"
					plainQuery = "SELECT i,s INTO " + plainName + " FROM qc_source"
				}
				run(fixture.Exec(contextQuery, 3))
				s := fixture.Prepared(fixture.Exec(plainQuery, 3))
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				run(fixture.QueryTableCreateBag(contextName))
				run(fixture.QueryTableCreateBag(plainName))
			}
			run(fixture.Exec("UPDATE qc_source SET i=99", 3))
			run(fixture.Exec("DROP TABLE qc_source RESTRICT", 0))
			for i := range 3 {
				name := fmt.Sprintf("qc_form%d", i)
				run(fixture.QueryTableCreateBag(name))
				run(fixture.Prepared(fixture.Exec("INSERT INTO "+name+" VALUES(3,'b')", 1)))
				run(fixture.Exec("UPDATE "+name+" SET i=4 WHERE i=3", 1))
				run(fixture.Exec("DELETE FROM "+name+" WHERE i=4", 1))
				run(fixture.QueryTableCreateBag(name))
			}
		})
	}
}

func TestReferenceQueryTableCreateCommandMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			fixture.QueryTableCreateSource(run)
			for form := range 2 {
				for route := range 4 {
					name := fmt.Sprintf("qc_command_%d_%d", form, route)
					q := "CREATE TABLE " + name + " AS SELECT i,s FROM qc_source"
					if form == 1 {
						q = "SELECT i,s INTO " + name + " FROM qc_source"
					}
					s := fixture.Select(q, []string{}, []string{}, nil)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 0 {
							t.Fatalf("command descriptor %v %v", types, err)
						}
					}
					if route == 3 {
						s.NonContext = true
						s.Prepared = true
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					} else if route == 2 {
						stmt, err := conn.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						fixture.QueryTableCreateAbsent(t, run, name)
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					} else if route == 0 {
						s.NonContext = true
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					} else {
						run(s)
					}
					run(fixture.QueryTableCreateBag(name))
				}
			}
			run(fixture.Exec("CREATE TABLE qc_empty AS SELECT i,s FROM qc_source WHERE false", 0))
			run(fixture.Select("SELECT * FROM qc_empty", []string{"i", "s"}, []string{"INT4", "TEXT"}, nil))
		})
	}
}

func TestReferenceQueryTableCreatePrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			for _, tc := range []struct {
				name, expression, typ string
				a, next               any
			}{
				{"qc_param_int", "$1::integer", "INT4", 7, 8},
				{"qc_param_text", "$1", "TEXT", "seven", "eight"},
			} {
				q := "CREATE TABLE " + tc.name + " AS SELECT " + tc.expression + " AS v"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer stmt.Close()
					fixture.QueryTableCreateAbsent(t, run, tc.name)
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
					for i, arg := range []any{tc.a, tc.next} {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, arg))
						want := arg
						if tc.typ == "INT4" {
							want = int64(arg.(int))
						}
						run(fixture.Select("SELECT v FROM "+tc.name, []string{"v"}, []string{tc.typ}, [][]any{{want}}))
						if i == 0 {
							failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P07", Message: `relation "` + tc.name + `" already exists`}, arg)
							failure.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
						}
						run(fixture.Exec("DROP TABLE "+tc.name, 0))
					}
				}()
			}
			fixture.QueryTableCreateSource(run)
			q := "CREATE TABLE qc_rebound AS SELECT * FROM qc_source"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run(fixture.Exec("INSERT INTO qc_source VALUES(3,'fresh')", 1))
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 4))
			run(fixture.Select("SELECT count(*) AS n FROM qc_rebound", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			run(fixture.Exec("DROP TABLE qc_rebound", 0))
			run(fixture.Exec("ALTER TABLE qc_source ADD COLUMN extra INTEGER DEFAULT 9", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 4))
			run(fixture.Select("SELECT extra FROM qc_rebound ORDER BY i", []string{"extra"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(9)}, {int64(9)}, {int64(9)}}))
		})
	}
}
