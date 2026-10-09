package reference_test

import "testing"

func TestReferenceRoleRetainedIdentityAcrossDropAndRecreate(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_original", "identity_later"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			roleScenarioCommand(t, c, "CREATE USER identity_original", 0, false)
			roleScenarioCommand(t, c, "CREATE USER identity_later", 0, false)
			roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION identity_original", 0, false)
			const q = "SELECT session_user,current_user,session_user"
			held, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			old, err := held.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			// Native owns an eager snapshot. pq must drain before transport reuse.
			if backend.pg {
				if !old.Next() {
					t.Fatal(old.Err())
				}
				var a, b, z string
				if err := old.Scan(&a, &b, &z); err != nil || a != "identity_original" || b != a || z != a {
					t.Fatal(a, b, z, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal(old.Err())
				}
				if err := old.Close(); err != nil {
					t.Fatal(err)
				}
			}
			roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
			roleScenarioCommand(t, c, "DROP ROLE identity_original", 0, false)
			roleScenarioCommand(t, c, "CREATE USER identity_original", 0, true)
			roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION identity_later", 0, false)
			roleScenarioIdentity(t, c, "identity_later", held, backend.pg)
			if !backend.pg {
				types, err := old.ColumnTypes()
				if err != nil || len(types) != 3 {
					t.Fatal(types, err)
				}
				for _, typ := range types {
					if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != roleScenarioNameScan(false) {
						t.Fatal(typ)
					}
				}
				if !old.Next() {
					t.Fatal(old.Err())
				}
				var a, b, z string
				if err := old.Scan(&a, &b, &z); err != nil || a != "identity_original" || b != a || z != a {
					t.Fatal(a, b, z, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal(old.Err())
				}
				if err := old.Close(); err != nil {
					t.Fatal(err)
				}
			}
			roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION identity_original", 0, true)
			roleScenarioIdentity(t, c, "identity_original", held, backend.pg)
			roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
		})
	}
}
