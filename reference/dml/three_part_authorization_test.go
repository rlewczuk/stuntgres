package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func threePartAuthorizationHeld(t *testing.T) {
	for _, update := range []bool{false, true} {
		name := "select"
		if update {
			name = "update"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"three_held_actor"})
			threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
				threePartSeed(t, p, b)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				for _, q := range []string{"CREATE ROLE three_held_actor", "GRANT USAGE ON SCHEMA qa TO three_held_actor", "GRANT SELECT(id,v),UPDATE(v) ON qa.items TO three_held_actor"} {
					run(fixture.Exec(q, 0))
				}
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION three_held_actor"); err != nil {
					t.Fatal(err)
				}
				defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				q := "SELECT qa.items.id,qa.items.v FROM qa.items WHERE qa.items.id=$1"
				privilege := "SELECT(v)"
				value := int64(10)
				if update {
					q = "UPDATE qa.items SET v=qa.items.v+1 WHERE qa.items.id=$1 RETURNING qa.items.id,qa.items.v"
					privilege = "UPDATE(v)"
					value = 11
				}
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}}, 1))
				run(fixture.Exec("REVOKE "+privilege+" ON qa.items FROM three_held_actor", 0))
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table items"}, 1)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
			})
		})
	}
}
