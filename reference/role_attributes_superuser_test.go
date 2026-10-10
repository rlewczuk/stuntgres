package reference_test

import (
	"fmt"
	"testing"
)

func roleAttributesSuperuserWitness(t *testing.T, prepared bool) {
	t.Run(fmt.Sprintf("superuser-bypass/prepared%v", prepared), func(t *testing.T) {
		roleReferenceCleanup(t, []string{"rau_owner", "rau_actor"})
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { roleAttributesRun(t, p, b, c, prepared, s) }
			for _, q := range []string{"CREATE ROLE rau_owner", "CREATE ROLE rau_actor", "SET SESSION AUTHORIZATION rau_owner"} {
				run(referenceExec(q, 0))
			}
			if b.pg {
				if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE,CREATE ON SCHEMA public TO rau_owner"); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("CREATE TABLE rau_secret(id INT)", 0))
			run(referenceExec("INSERT INTO rau_secret VALUES(7)", 1))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			if b.pg {
				if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO rau_actor"); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("SET SESSION AUTHORIZATION rau_actor", 0))
			denied := roleReferenceError("SELECT id FROM rau_secret", "42501", "permission denied for table rau_secret", "", prepared, "execute")
			denied.selectRows = true
			run(denied)
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("ALTER ROLE rau_actor SUPERUSER", 0))
			run(referenceExec("SET SESSION AUTHORIZATION rau_actor", 0))
			check := referenceSelect("SELECT id FROM rau_secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			run(check)
			held, err := c.PrepareContext(p.ctx, "SELECT id FROM rau_secret")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			// Bootstrap alters the descriptor while this physical actor and held
			// statement remain alive; fresh authority, not Prepare-time privilege.
			roleAttributesRun(t, p, b, b.db, prepared, referenceExec("ALTER ROLE rau_actor NOSUPERUSER", 0))
			var id int64
			err = held.QueryRowContext(p.ctx).Scan(&id)
			referenceAssertDiagnostic(t, err, denied.diagnostic)
			run(denied)
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(check)
		}
	})
}
