package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBooleanCallRelationList(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bcp_list_owner"})
	for _, schema := range []string{"bcp_list_a", "bcp_list_b", "bcp_list_off"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			step := func(rows [][]any) fixture.Step {
				return fixture.Select(fixture.BooleanCallRelationList, []string{"Schema", "Name", "Type", "Owner"}, []string{"NAME", "NAME", "TEXT", "NAME"}, rows)
			}
			run(fixture.Exec("SET search_path=public", 0))
			run(step(nil))
			run(fixture.Prepared(step(nil)))
			for _, q := range fixture.BooleanCallRelationSetup() {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("SET search_path=bcp_list_a,bcp_list_b,pg_catalog", 0))
			stmt, err := conn.PrepareContext(p.Ctx, fixture.BooleanCallRelationList)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, reverse := range []bool{false, true, false} {
				path := "bcp_list_a,bcp_list_b,pg_catalog"
				if reverse {
					path = "bcp_list_b,bcp_list_a,pg_catalog"
				}
				run(fixture.Exec("SET search_path="+path, 0))
				s := step(fixture.BooleanCallRelationRows(reverse))
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: fixture.BooleanCallRelationList}, b.Recorder, s)
			}
		})
	}
}
