package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Table and column authority is shared; the separate hierarchy control retains
// its PostgreSQL-only inheritance-descriptor boundary.
func TestReferenceLockModesPrivilegeControls(t *testing.T) {
	const role = "lock_modes_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := pair.Ctx
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			t.Cleanup(func() {
				a.ExecContext(ctx, "ROLLBACK")
				b.ExecContext(ctx, "ROLLBACK")
				b.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			})
			lockModesExec(t, ctx, a, "CREATE ROLE "+role+" LOGIN")
			if backend.PG {
				lockModesExec(t, ctx, a, "GRANT USAGE, CREATE ON SCHEMA public TO "+role)
			}
			lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
			lockModesExec(t, ctx, a, "CREATE SEQUENCE seq")
			lockModesExec(t, ctx, a, "CREATE VIEW v AS SELECT v FROM t")
			lockModesExec(t, ctx, a, "CREATE VIEW vi WITH (security_invoker=true) AS SELECT v FROM t")
			for _, right := range []struct {
				name    string
				allowed int
			}{
				{"SELECT", 1}, {"INSERT", 3}, {"UPDATE", 8}, {"DELETE", 8}, {"TRUNCATE", 8}, {"MAINTAIN", 8},
			} {
				t.Run(right.name, func(t *testing.T) {
					lockModesExec(t, ctx, a, "REVOKE ALL ON t FROM "+role)
					lockModesExec(t, ctx, a, "GRANT "+right.name+" ON t TO "+role)
					lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION "+role)
					for i, mode := range lockModesNames {
						code, message := "", ""
						if i >= right.allowed {
							code = "42501"
							message = "permission denied for table t"
						}
						lockModesProbe(t, ctx, b, "LOCK t IN "+mode+" MODE NOWAIT", code, message)
					}
					lockModesExec(t, ctx, b, "RESET SESSION AUTHORIZATION")
				})
			}
			lockModesExec(t, ctx, a, "REVOKE ALL ON t FROM "+role)
			lockModesExec(t, ctx, a, "GRANT UPDATE(v) ON t TO "+role)
			lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION "+role)
			lockModesProbe(t, ctx, b, "LOCK t IN ACCESS SHARE MODE NOWAIT", "42501", "permission denied for table t")
			lockModesExec(t, ctx, b, "BEGIN")
			_, err := b.ExecContext(ctx, "LOCK seq")
			lockModesDiagnostic(t, err, "42809", `cannot lock relation "seq"`, "This operation is not supported for sequences.")
			lockModesExec(t, ctx, b, "ROLLBACK")
			lockModesProbe(t, ctx, b, "LOCK v", "42501", "permission denied for view v")
			lockModesExec(t, ctx, b, "RESET SESSION AUTHORIZATION")
			lockModesExec(t, ctx, a, "GRANT UPDATE ON v, vi TO "+role)
			lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION "+role)
			lockModesProbe(t, ctx, b, "LOCK v IN SHARE MODE NOWAIT", "", "")
			lockModesProbe(t, ctx, b, "LOCK vi IN SHARE MODE NOWAIT", "42501", "permission denied for table t")
			fixture.WithPublicSchemaOwner(t, pair, backend, role, func() { lockModesExec(t, ctx, b, "CREATE TABLE owned(v INT)") })
			lockModesProbe(t, ctx, b, "LOCK owned NOWAIT", "", "")
			lockModesExec(t, ctx, b, "REVOKE ALL ON owned FROM "+role)
			lockModesProbe(t, ctx, b, "LOCK owned IN ACCESS SHARE MODE NOWAIT", "42501", "permission denied for table owned")
			lockModesExec(t, ctx, b, "RESET SESSION AUTHORIZATION")
			lockModesExec(t, ctx, a, "GRANT DELETE ON t TO PUBLIC")
			lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION "+role)
			lockModesProbe(t, ctx, b, "LOCK t NOWAIT", "", "")
			lockModesExec(t, ctx, b, "RESET SESSION AUTHORIZATION")
			// Same-object permission failure beats NOWAIT contention.
			lockModesExec(t, ctx, a, "REVOKE DELETE ON t FROM PUBLIC")
			lockModesExec(t, ctx, a, "BEGIN")
			lockModesExec(t, ctx, a, "LOCK t")
			lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION "+role)
			lockModesProbe(t, ctx, b, "LOCK t NOWAIT", "42501", "permission denied for table t")
			lockModesExec(t, ctx, b, "RESET SESSION AUTHORIZATION")
			lockModesExec(t, ctx, a, "ROLLBACK")
		})
	}
}

func TestReferenceLockModesHierarchyControls(t *testing.T) {
	const role = "lock_modes_parent_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		if !backend.PG {
			continue // Native tables have no inheritance descriptors.
		}
		t.Run(backend.Name, func(t *testing.T) {
			ctx := pair.Ctx
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			t.Cleanup(func() {
				a.ExecContext(ctx, "ROLLBACK")
				b.ExecContext(ctx, "ROLLBACK")
				b.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			})
			lockModesExec(t, ctx, a, "CREATE TABLE parent(v INT)")
			lockModesExec(t, ctx, a, "CREATE TABLE child() INHERITS(parent)")
			lockModesExec(t, ctx, a, "CREATE TABLE grandchild() INHERITS(child)")
			lockModesExec(t, ctx, a, "BEGIN")
			defer a.ExecContext(ctx, "ROLLBACK")
			lockModesExec(t, ctx, a, "LOCK grandchild")
			for _, target := range []string{"ONLY parent", "ONLY(parent)"} {
				lockModesProbe(t, ctx, b, "LOCK "+target+" IN ACCESS SHARE MODE NOWAIT", "", "")
			}
			for _, target := range []string{"parent", "parent *", "ONLY child, parent *"} {
				lockModesProbe(t, ctx, b, "LOCK "+target+" IN ACCESS SHARE MODE NOWAIT", "55P03", `could not obtain lock on relation "grandchild"`)
			}
			lockModesExec(t, ctx, a, "ROLLBACK")
			lockModesExec(t, ctx, a, "CREATE ROLE "+role+" LOGIN")
			lockModesExec(t, ctx, a, "GRANT USAGE ON SCHEMA public TO "+role)
			lockModesExec(t, ctx, a, "GRANT SELECT ON parent TO "+role)
			lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION "+role)
			lockModesProbe(t, ctx, b, "LOCK parent IN ACCESS SHARE MODE NOWAIT", "", "")
			lockModesProbe(t, ctx, b, "LOCK child IN ACCESS SHARE MODE NOWAIT", "42501", "permission denied for table child")
			lockModesExec(t, ctx, b, "RESET SESSION AUTHORIZATION")
		})
	}
}
