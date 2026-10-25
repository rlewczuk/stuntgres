package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesDependencyClosure(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE DOMAIN tt_d AS integer CHECK(VALUE>0)", "CREATE DOMAIN tt_child AS tt_d", "CREATE TABLE tt_target(id tt_d PRIMARY KEY,keep integer)", "CREATE TABLE tt_foreign(id integer REFERENCES tt_target(id),keep integer)", "CREATE VIEW tt_live AS SELECT id FROM tt_target", "CREATE TABLE tt_recipe(v integer CHECK(v>1::tt_d),keep integer)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO tt_target VALUES(7,11)", 1))
			run(fixture.Exec("INSERT INTO tt_foreign VALUES(7,12)", 1))
			run(fixture.Exec("INSERT INTO tt_recipe VALUES(7,13)", 1))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("SAVEPOINT closure", 0))
			d := fixture.ControlsReferenceDiagnostic("2BP01", `cannot drop type tt_d because other objects depend on it`)
			d.Detail = "type tt_child depends on type tt_d\ncolumn id of table tt_target depends on type tt_d\nconstraint tt_foreign_id_fkey on table tt_foreign depends on column id of table tt_target\nview tt_live depends on column id of table tt_target\nconstraint tt_recipe_v_check on table tt_recipe depends on type tt_d"
			d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic("DROP DOMAIN tt_d RESTRICT", d))
			run(fixture.Exec("ROLLBACK TO closure", 0))
			run(fixture.Exec("DROP DOMAIN tt_d CASCADE", 0))
			run(fixture.Select("SELECT keep FROM tt_target", []string{"keep"}, []string{"INT4"}, [][]any{{int64(11)}}))
			run(fixture.Select("SELECT id,keep FROM tt_foreign", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(12)}}))
			run(fixture.Select("SELECT v,keep FROM tt_recipe", []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(13)}}))
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tt_live'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("INSERT INTO tt_foreign VALUES(99,14)", 1))
			run(fixture.Exec("INSERT INTO tt_recipe VALUES(-1,15)", 1))
			run(fixture.Exec("ROLLBACK TO closure", 0))
			run(fixture.Select("SELECT id FROM tt_live", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("ROLLBACK", 0))
		})
	}
}

func TestReferenceTransactionalTypesHeldSignature(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE TYPE tt_pair AS(id integer)", "CREATE TABLE tt_typed OF tt_pair"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO tt_typed VALUES(7)", 1))
			q := "SELECT * FROM tt_typed"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			before := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, before)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("ALTER TYPE tt_pair ADD ATTRIBUTE note text CASCADE", 0))
			run(fixture.Exec("SAVEPOINT signature", 0))
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("ROLLBACK TO signature", 0))
			run(fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, before)
		})
	}
}
