package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleAttributesFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ras_creator", "ras_target", "ras_unrelated"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { roleAttributesRun(t, p, b, b.DB, false, s) }
		run(fixture.Exec("CREATE USER ras_creator CREATEROLE PASSWORD 'role-attributes-fixture' VALID UNTIL 'infinity'", 0))
		run(fixture.Exec("CREATE ROLE ras_unrelated", 0))
		pool := roleAttributesPool(t, p, b, "ras_creator")
		creator, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer creator.Close()
		actor := func(s fixture.Step) { roleAttributesRun(t, p, b, creator, false, s) }
		actor(fixture.Exec("CREATE ROLE ras_target", 0))
		actor(fixture.Prepared(fixture.Exec("ALTER USER ras_target LOGIN CONNECTION LIMIT 1 PASSWORD 'role-attributes-fixture'", 0)))
		first, err := roleAttributesPhysical(t, p, b, "ras_target")
		if err != nil {
			t.Fatal(err)
		}
		defer first.Close()
		second, err := roleAttributesPhysical(t, p, b, "ras_target")
		if second != nil {
			second.Close()
			t.Fatal("second limited admission succeeded")
		}
		want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: `too many connections for role "ras_target"`}
		fixture.AssertDiagnostic(t, err, &want)
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		replacement, err := roleAttributesPhysical(t, p, b, "ras_target")
		if err != nil {
			t.Fatal(err)
		}
		defer replacement.Close()
		actor(roleAttributesDenied("ALTER ROLE ras_unrelated LOGIN", `Only roles with the CREATEROLE attribute and the ADMIN option on role "ras_unrelated" may alter this role.`, false))
		held, err := creator.PrepareContext(p.Ctx, "ALTER ROLE ras_target NOINHERIT")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		result, err := held.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			t.Fatal("held ALTER count", n, err)
		}
		run(fixture.Exec("ALTER ROLE ras_creator NOCREATEROLE", 0))
		result, err = held.ExecContext(p.Ctx)
		if result != nil {
			t.Fatal("denied held ALTER returned result")
		}
		denied := roleAttributesDenied("ALTER ROLE ras_target NOINHERIT", `Only roles with the CREATEROLE attribute and the ADMIN option on role "ras_target" may alter this role.`, false)
		fixture.AssertDiagnostic(t, err, denied.Diagnostic)
		run(fixture.Exec("ALTER USER ras_creator NOLOGIN", 0))
		roleScenarioIdentity(t, creator, "ras_creator", nil, b.PG)
		newCreator, err := roleAttributesPhysical(t, p, b, "ras_creator")
		if newCreator != nil {
			newCreator.Close()
		}
		noLogin := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: `role "ras_creator" is not permitted to log in`}
		fixture.AssertDiagnostic(t, err, &noLogin)
		if b.PG {
			roleAttributesFacts(t, p, "ras_creator", []any{false, false, false, false, true, false, false, int64(-1), false, "infinity"})
		}
	}
}
