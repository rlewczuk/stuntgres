package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainNonExecution(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE SEQUENCE explain_s START 1", 0))
					run(fixture.Exec("CREATE TABLE explain_d(id integer DEFAULT nextval('explain_s'))", 0))
					run(fixture.Exec("CREATE TABLE explain_state(id integer, v text)", 0))
					run(fixture.Exec("INSERT INTO explain_state VALUES (1,'one'),(2,'two'),(3,'three')", 3))
					empty := func() {
						t.Helper()
						run(fixture.Select("SELECT id FROM explain_d ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
					}
					state := func() {
						t.Helper()
						run(fixture.Select("SELECT id,v FROM explain_state ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
					}
					// Held handles are executed twice: both Prepare and each Execute
					// must avoid defaults, volatile projections, and auxiliary DML.
					explain := func(query string) {
						t.Helper()
						if !prepared {
							for range 2 {
								explainQuery(t, p, b, query, false)
							}
							return
						}
						stmt, err := b.DB.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for range 2 {
							rows, err := stmt.QueryContext(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							explainTextRows(t, rows)
						}
					}
					explain("EXPLAIN INSERT INTO explain_d DEFAULT VALUES RETURNING id")
					empty()
					run(fixture.Select("SELECT nextval('explain_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					explain("EXPLAIN SELECT nextval('explain_s')")
					run(fixture.Select("SELECT nextval('explain_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					explain("EXPLAIN WITH w AS (INSERT INTO explain_d VALUES (99) RETURNING id) SELECT * FROM w")
					empty()
					explain("EXPLAIN SELECT nextval('explain_s'), generate_series(1,3)")
					explain("EXPLAIN SELECT * FROM generate_series(1,nextval('explain_s'))")
					run(fixture.Select("SELECT nextval('explain_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
					for _, query := range []string{
						"EXPLAIN INSERT INTO explain_state VALUES (4,'four') RETURNING nextval('explain_s')",
						"EXPLAIN UPDATE explain_state SET v='changed' WHERE id=2 RETURNING nextval('explain_s')",
						"EXPLAIN DELETE FROM explain_state WHERE id=1 RETURNING nextval('explain_s')",
						"EXPLAIN MERGE INTO explain_state t USING (VALUES (2,'changed'),(4,'four')) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v) RETURNING nextval('explain_s')",
						"EXPLAIN WITH w AS (UPDATE explain_state SET v='changed' RETURNING id) SELECT * FROM w",
						"EXPLAIN WITH w AS (DELETE FROM explain_state RETURNING id) SELECT * FROM w",
					} {
						explain(query)
						state()
						empty()
					}
					run(fixture.Select("SELECT nextval('explain_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
					// A valid modifying prefix followed by junk must not be run.
					for _, query := range []string{
						"EXPLAIN INSERT INTO explain_d DEFAULT VALUES RETURNING id +",
						"EXPLAIN WITH w AS (INSERT INTO explain_d VALUES(99) RETURNING id) SELECT * FROM w WHERE",
					} {
						s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(len([]rune(query)) + 1)})
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
						empty()
						state()
					}
					run(fixture.Select("SELECT nextval('explain_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
					// The same default and target remain live for a real mutation.
					run(fixture.Exec("INSERT INTO explain_d DEFAULT VALUES", 1))
					run(fixture.Select("SELECT id FROM explain_d ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))
					run(fixture.Exec("UPDATE explain_state SET v='changed' WHERE id=2", 1))
					run(fixture.Select("SELECT id,v FROM explain_state ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "changed"}, {int64(3), "three"}}))
				})
			}
		})
	}
}
