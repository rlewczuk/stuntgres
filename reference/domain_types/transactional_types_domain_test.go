package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTypesDomain(t *testing.T) {
	for _, mode := range []string{"own_insert", "replace", "delete", "null"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE DOMAIN tt_d AS integer DEFAULT 7", "CREATE DOMAIN tt_child AS tt_d", "CREATE TABLE tt_data(id integer,v tt_child)", "CREATE TABLE tt_override(v tt_d DEFAULT 11)"} {
						run(fixture.Exec(q, 0))
					}
					if mode == "replace" || mode == "delete" {
						run(fixture.Exec("INSERT INTO tt_data VALUES(1,-1)", 1))
					}
					if mode == "null" {
						run(fixture.Exec("INSERT INTO tt_data VALUES(1,NULL)", 1))
					}
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					switch mode {
					case "own_insert":
						run(fixture.Exec("INSERT INTO tt_data VALUES(1,-1)", 1))
					case "replace":
						run(fixture.Exec("UPDATE tt_data SET v=1 WHERE id=1", 1))
					case "delete":
						run(fixture.Exec("DELETE FROM tt_data WHERE id=1", 1))
					case "null":
						run(fixture.Exec("DELETE FROM tt_data WHERE id=1", 1))
						run(fixture.Exec("INSERT INTO tt_data VALUES(2,NULL)", 1))
					}
					run(fixture.Exec("SAVEPOINT validation", 0))
					if mode == "own_insert" {
						d := fixture.ControlsReferenceDiagnostic("23514", `column "v" of table "tt_data" contains values that violate the new constraint`)
						d.Schema, d.Table, d.Column = "public", "tt_data", "v"
						run(fixture.ErrorDiagnostic("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)", d))
						run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						run(fixture.Exec("ROLLBACK TO validation", 0))
						run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", 0))
						run(fixture.Exec("SAVEPOINT invalid", 0))
						run(fixture.ErrorDiagnostic("ALTER DOMAIN tt_d VALIDATE CONSTRAINT positive", d))
						run(fixture.Exec("ROLLBACK TO invalid", 0))
						run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE contypid='tt_d'::regtype AND conname='positive'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
						run(fixture.Exec("UPDATE tt_data SET v=1", 1))
						run(fixture.Exec("ALTER DOMAIN tt_d VALIDATE CONSTRAINT positive", 0))
					} else {
						run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)", 0))
					}
					if mode == "null" {
						run(fixture.Exec("SAVEPOINT nn", 0))
						d := fixture.ControlsReferenceDiagnostic("23502", `column "v" of table "tt_data" contains null values`)
						d.Schema, d.Table, d.Column = "public", "tt_data", "v"
						run(fixture.ErrorDiagnostic("ALTER DOMAIN tt_d SET NOT NULL", d))
						run(fixture.Exec("ROLLBACK TO nn", 0))
						run(fixture.Exec("UPDATE tt_data SET v=2", 1))
					}
					run(fixture.Exec("ALTER DOMAIN tt_d SET NOT NULL", 0))
					run(fixture.Exec("ALTER DOMAIN tt_d SET NOT NULL", 0))
					run(fixture.Exec("ALTER DOMAIN tt_d DROP NOT NULL", 0))
					run(fixture.Exec("ALTER DOMAIN tt_d SET DEFAULT 9", 0))
					run(fixture.Exec("CREATE TABLE tt_future(v tt_d)", 0))
					run(fixture.Exec("INSERT INTO tt_future DEFAULT VALUES", 1))
					run(fixture.Exec("INSERT INTO tt_data(id) VALUES(3)", 1))
					run(fixture.Exec("INSERT INTO tt_override DEFAULT VALUES", 1))
					run(fixture.Exec("ALTER DOMAIN tt_d DROP DEFAULT", 0))
					run(fixture.Exec("INSERT INTO tt_future DEFAULT VALUES", 1))
					run(fixture.Select("SELECT v FROM tt_future ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}, {nil}}))
					run(fixture.Select("SELECT v FROM tt_data WHERE id=3", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(fixture.Select("SELECT v FROM tt_override", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT typdefault,typnotnull FROM pg_type WHERE oid='tt_d'::regtype", []string{"typdefault", "typnotnull"}, []string{"TEXT", "BOOL"}, [][]any{{"7", false}}))
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE contypid='tt_d'::regtype", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			}
		})
	}
	t.Run("selected_check_and_revalidation", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE SEQUENCE tt_calls", "CREATE DOMAIN tt_d AS integer", "CREATE TABLE tt_data(v tt_d)"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO tt_data VALUES(1),(NULL)", 2))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT z_unrelated CHECK(FALSE) NOT VALID", 0))
				run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT a_count CHECK(nextval('tt_calls')>0)", 0))
				run(fixture.Select("SELECT last_value FROM tt_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
				run(fixture.Exec("ALTER DOMAIN tt_d VALIDATE CONSTRAINT a_count", 0))
				run(fixture.Select("SELECT last_value FROM tt_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.Exec("ROLLBACK", 0))
			})
		}
	})
}
