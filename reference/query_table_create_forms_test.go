package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceQueryTableCreateForms(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			_, run := queryTableCreateSession(t, p, b)
			queryTableCreateSource(run)
			for i, q := range []string{
				"CREATE TABLE qc_form0 AS SELECT i,s FROM qc_source",
				"CREATE TABLE qc_form1 AS SELECT i,s FROM qc_source WITH DATA",
				"SELECT i,s INTO qc_form2 FROM qc_source",
			} {
				s := referenceExec(q, 3)
				// Actual plain DB consumer; the fresh Context route has its own target.
				s.nonContext = true
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				run(queryTableCreateBag(fmt.Sprintf("qc_form%d", i)))
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
				run(referenceExec(contextQuery, 3))
				s := referencePrepared(referenceExec(plainQuery, 3))
				s.nonContext = true
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				run(queryTableCreateBag(contextName))
				run(queryTableCreateBag(plainName))
			}
			run(referenceExec("UPDATE qc_source SET i=99", 3))
			run(referenceExec("DROP TABLE qc_source RESTRICT", 0))
			for i := range 3 {
				name := fmt.Sprintf("qc_form%d", i)
				run(queryTableCreateBag(name))
				run(referencePrepared(referenceExec("INSERT INTO "+name+" VALUES(3,'b')", 1)))
				run(referenceExec("UPDATE "+name+" SET i=4 WHERE i=3", 1))
				run(referenceExec("DELETE FROM "+name+" WHERE i=4", 1))
				run(queryTableCreateBag(name))
			}
		})
	}
}

func TestReferenceQueryTableCreateCommandMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			queryTableCreateSource(run)
			for form := range 2 {
				for route := range 4 {
					name := fmt.Sprintf("qc_command_%d_%d", form, route)
					q := "CREATE TABLE " + name + " AS SELECT i,s FROM qc_source"
					if form == 1 {
						q = "SELECT i,s INTO " + name + " FROM qc_source"
					}
					s := referenceSelect(q, []string{}, []string{}, nil)
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 0 {
							t.Fatalf("command descriptor %v %v", types, err)
						}
					}
					if route == 3 {
						s.nonContext = true
						s.prepared = true
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					} else if route == 2 {
						stmt, err := conn.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						queryTableCreateAbsent(t, run, b.pg, name)
						referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, b.recorder, s)
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					} else if route == 0 {
						s.nonContext = true
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					} else {
						run(s)
					}
					run(queryTableCreateBag(name))
				}
			}
			run(referenceExec("CREATE TABLE qc_empty AS SELECT i,s FROM qc_source WHERE false", 0))
			run(referenceSelect("SELECT * FROM qc_empty", []string{"i", "s"}, []string{"INT4", "TEXT"}, nil))
		})
	}
}

func TestReferenceQueryTableCreatePrepared(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			for _, tc := range []struct {
				name, expression, typ string
				a, next               any
			}{
				{"qc_param_int", "$1::integer", "INT4", 7, 8},
				{"qc_param_text", "$1", "TEXT", "seven", "eight"},
			} {
				q := "CREATE TABLE " + tc.name + " AS SELECT " + tc.expression + " AS v"
				stmt, err := conn.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer stmt.Close()
					queryTableCreateAbsent(t, run, b.pg, tc.name)
					held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
					for i, arg := range []any{tc.a, tc.next} {
						referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, arg))
						want := arg
						if tc.typ == "INT4" {
							want = int64(arg.(int))
						}
						run(referenceSelect("SELECT v FROM "+tc.name, []string{"v"}, []string{tc.typ}, [][]any{{want}}))
						if i == 0 {
							failure := referenceImplicitError(q, referenceDiagnostic{Code: "42P07", Message: `relation "` + tc.name + `" already exists`}, arg)
							failure.errorStage = "execute"
							referenceRunStep(t, p.ctx, held, b.recorder, failure)
						}
						run(referenceExec("DROP TABLE "+tc.name, 0))
					}
				}()
			}
			queryTableCreateSource(run)
			q := "CREATE TABLE qc_rebound AS SELECT * FROM qc_source"
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run(referenceExec("INSERT INTO qc_source VALUES(3,'fresh')", 1))
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 4))
			run(referenceSelect("SELECT count(*) AS n FROM qc_rebound", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			run(referenceExec("DROP TABLE qc_rebound", 0))
			run(referenceExec("ALTER TABLE qc_source ADD COLUMN extra INTEGER DEFAULT 9", 0))
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 4))
			run(referenceSelect("SELECT extra FROM qc_rebound ORDER BY i", []string{"extra"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(9)}, {int64(9)}, {int64(9)}}))
		})
	}
}
