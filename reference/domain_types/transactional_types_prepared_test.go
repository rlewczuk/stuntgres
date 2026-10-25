package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesPrepared(t *testing.T) {
	for _, cast := range []string{"$1::tt_d", "($1::tt_d[])[1]", "($1::_tt_d)[1]"} {
		t.Run(cast, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE DOMAIN tt_d AS integer", 0))
					run(fixture.Exec("CREATE TABLE tt_data(v tt_d)", 0))
					q := "SELECT (" + cast + ")::integer AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					arg := "7"
					if strings.Contains(cast, "[") {
						arg = "{7}"
					}
					check := func() {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, arg))
					}
					check()
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)", 0))
					check()
					run(fixture.Exec("SAVEPOINT rules", 0))
					d := fixture.ControlsReferenceDiagnostic("23514", `value for domain tt_d violates check constraint "positive"`)
					d.Schema, d.DataType, d.Constraint = "public", "tt_d", "positive"
					bad := "-1"
					if strings.Contains(cast, "[") {
						bad = "{-1}"
					}
					d.Context = "unnamed portal parameter $1 = '...'"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, bad))
					run(fixture.Exec("ROLLBACK TO rules", 0))
					run(fixture.Exec("ROLLBACK", 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}, bad))
				})
			}
		})
	}
	t.Run("fresh_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tt_actor"})
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE DOMAIN tt_d AS integer", "CREATE ROLE tt_actor", "GRANT USAGE ON SCHEMA public TO tt_actor"} {
					run(fixture.Exec(q, 0))
				}
				q := "ALTER DOMAIN tt_d SET DEFAULT 9"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SET LOCAL ROLE tt_actor", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of type tt_d")))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET ROLE", 0))
			})
		}
	})
}

func TestReferenceTransactionalTypesDiagnostics(t *testing.T) {
	for _, a := range transactionalTypeActions() {
		t.Run(a.name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tt_dest")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range append([]string{"CREATE SCHEMA tt_dest", "CREATE DOMAIN tt_d AS integer", "CREATE TYPE tt_pair AS(id integer,note text)", "CREATE TABLE tt_typed OF tt_pair"}, a.setup...) {
						run(fixture.Exec(q, 0))
					}
					stmt, err := c.PrepareContext(p.Ctx, a.query)
					if err != nil {
						t.Fatal("quiet utility Prepare", err)
					}
					defer stmt.Close()
					run(fixture.Exec("BEGIN READ ONLY", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					words := strings.Fields(a.query)
					tag := strings.Join(words[:2], " ")
					s := fixture.ErrorDiagnostic(a.query, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction"))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: a.query}, b.Recorder, s)
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK", 0))
				})
			}
		})
	}
}
