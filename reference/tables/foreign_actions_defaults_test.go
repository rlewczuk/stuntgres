package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsDefaults(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		for _, prepared := range []bool{false, true} {
			for _, def := range []string{"1", "", "0", "9"} {
				t.Run(fmt.Sprintf("%s/default=%s/prepared=%t", direction, def, prepared), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
						clause := ""
						if def != "" {
							clause = " DEFAULT " + def
						}
						run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT"+clause+" CONSTRAINT fk REFERENCES p ON "+direction+" SET DEFAULT,note TEXT DEFAULT (1/0)::text)", 0))
						run(fixture.Exec("INSERT INTO p VALUES(0),(1),(2)", 3))
						old := int64(2)
						if def == "0" {
							old = 0
						}
						run(fixture.Exec("INSERT INTO c VALUES(10,$1,'first'),(11,$1,'second')", 2, old))
						q := fmt.Sprintf("DELETE FROM p WHERE id=%d", old)
						if direction == "UPDATE" {
							q = fmt.Sprintf("UPDATE p SET id=3 WHERE id=%d", old)
						}
						if def == "0" || def == "9" {
							d := fixture.ForeignKeyParent("p", "c", "fk", `Key (id)=(0) is still referenced from table "c".`)
							if def == "9" {
								d = fixture.ForeignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`)
							}
							run(fixture.ForeignKeyFail(q, d, prepared))
							run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 0, 1, 2))
						} else {
							run(foreignActionsExec(q, 1, prepared))
						}
						var value any = old
						if def == "1" {
							value = int64(1)
						}
						if def == "" {
							value = nil
						}
						run(fixture.Select("SELECT id,pid,note FROM c ORDER BY id", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(10), value, "first"}, {int64(11), value, "second"}}))
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsDefaultsPerRow(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE SEQUENCE action_defaults START 10", 0))
			run(fixture.Exec("CREATE TABLE p(id BIGINT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(id INT,pid BIGINT DEFAULT nextval('action_defaults') CONSTRAINT fk REFERENCES p ON "+direction+" SET DEFAULT)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(2),(10),(11)", 3))
			run(fixture.Exec("INSERT INTO c VALUES(1,2),(2,2)", 2))
			// Prepare and Dump must not consume a generator. Parent2 has two
			// matching rows; the two demanded values are independently10 and11.
			q := "DELETE FROM p WHERE id=2"
			if direction == "UPDATE" {
				q = "UPDATE p SET id=3 WHERE id=2"
			}
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if !b.PG {
				_ = fixture.CheckDump(t, p)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1))
			s := fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(11)}})
			s.Bag = true
			run(s)
			run(fixture.Select("SELECT last_value FROM action_defaults", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(11)}}))
		})
	}
}

func TestReferenceForeignActionsDefaultsFailures(t *testing.T) {
	for _, tc := range []struct{ typ, def, old, code, message string }{
		{"INT", "1/0", "2", "22012", "division by zero"},
		{"INT", "'oops'::text::int", "2", "22P02", `invalid input syntax for type integer: "oops"`},
		{"VARCHAR(2)", "'long'::text", "'ok'", "22001", "value too long for type character varying(2)"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.code, prepared), func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id "+tc.typ+" PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(id INT,pid "+tc.typ+" DEFAULT "+tc.def+" CONSTRAINT fk REFERENCES p ON DELETE SET DEFAULT)", 0))
					run(fixture.Exec("INSERT INTO p VALUES("+tc.old+")", 1))
					run(fixture.Exec("INSERT INTO c VALUES(1,"+tc.old+")", 1))
					query := foreignActionsSQL("c", "pid", "SET DEFAULT", "DELETE")
					if tc.typ == "VARCHAR(2)" {
						query = `UPDATE ONLY "public"."c" SET "pid" = DEFAULT WHERE $1::pg_catalog.text OPERATOR(pg_catalog.=) "pid"::pg_catalog.text`
					}
					d := foreignActionsContext(fixture.ForeignKeyError(tc.code, tc.message, "", "", ""), query)
					run(fixture.ForeignKeyFail("DELETE FROM p", d, prepared))
					typ := "INT4"
					var old any = int64(2)
					if tc.typ == "VARCHAR(2)" {
						typ, old = "VARCHAR", "ok"
					}
					run(fixture.Select("SELECT id FROM p", []string{"id"}, []string{typ}, [][]any{{old}}))
					run(fixture.Select("SELECT id,pid FROM c", []string{"id", "pid"}, []string{"INT4", typ}, [][]any{{int64(1), old}}))
				})
			})
		}
	}
}
