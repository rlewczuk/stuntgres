package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func roleAttributesSuperuserWitness(t *testing.T, prepared bool) {
	t.Run(fmt.Sprintf("superuser-bypass/prepared%v", prepared), func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"rau_owner", "rau_actor"})
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { roleAttributesRun(t, p, b, c, prepared, s) }
			for _, q := range []string{"CREATE ROLE rau_owner", "CREATE ROLE rau_actor", "SET SESSION AUTHORIZATION rau_owner"} {
				run(fixture.Exec(q, 0))
			}
			if b.PG {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE,CREATE ON SCHEMA public TO rau_owner"); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("CREATE TABLE rau_secret(id INT)", 0))
			run(fixture.Exec("INSERT INTO rau_secret VALUES(7)", 1))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if b.PG {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO rau_actor"); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION rau_actor", 0))
			denied := fixture.RoleReferenceError("SELECT id FROM rau_secret", "42501", "permission denied for table rau_secret", "", prepared, "execute")
			denied.SelectRows = true
			run(denied)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("ALTER ROLE rau_actor SUPERUSER", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rau_actor", 0))
			check := fixture.Select("SELECT id FROM rau_secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			run(check)
			held, err := c.PrepareContext(p.Ctx, "SELECT id FROM rau_secret")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			// Bootstrap alters the descriptor while this physical actor and held
			// statement remain alive; fresh authority, not Prepare-time privilege.
			roleAttributesRun(t, p, b, b.DB, prepared, fixture.Exec("ALTER ROLE rau_actor NOSUPERUSER", 0))
			var id int64
			err = held.QueryRowContext(p.Ctx).Scan(&id)
			fixture.AssertDiagnostic(t, err, denied.Diagnostic)
			run(denied)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(check)
		}
	})
}
