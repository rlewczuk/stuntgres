package fixture

import (
	"database/sql"
	"testing"
)

// Each route starts from an independent catalog. Expectations precede backend
// comparison; utilities and consumers both traverse the actual public driver.
func RoleGrantScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn, func(Step), bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			RoleReferenceCleanup(t, []string{"rg_reader", "rg_owner", "rg_a", "rg_b", "rg_c", "rg_group", "rg_future"})
			p := NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s Step) {
						s.Prepared = prepared
						if !t.Run(s.Query, func(t *testing.T) {
							RunStep(t, p.Ctx, conn, backend.Recorder, s)
						}) {
							t.FailNow()
						}
					}
					if backend.PG {
						run(Exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
					}
					for _, q := range []string{"CREATE ROLE rg_reader", "CREATE ROLE rg_owner", "CREATE ROLE rg_a", "CREATE ROLE rg_b", "CREATE ROLE rg_c", "CREATE ROLE rg_group"} {
						run(Exec(q, 0))
					}
					body(t, p, backend, conn, run, prepared)
				})
			}
		})
	}
}

func RoleGrantDenied(q, object string) Step {
	return ImplicitError(q, Diagnostic{Code: "42501", Message: "permission denied for table " + object})
}
