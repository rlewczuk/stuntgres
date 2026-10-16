package fixture

import (
	"database/sql"
	"strings"
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
						run(Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
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

// Shared object-grant scenarios preserve the same public session and diagnostic routes.
func RoleGrantActor(run func(Step), role string, body func()) {
	run(Exec("SET SESSION AUTHORIZATION "+role, 0))
	body()
	run(Exec("RESET SESSION AUTHORIZATION", 0))
}

func RoleGrantPermissionDenied(run func(Step), q, kind, name string, prepared ...bool) {
	d := Diagnostic{Code: "42501", Message: "permission denied for " + kind + " " + name}
	stage := "execute"
	if kind == "schema" && strings.HasPrefix(q, "SELECT") {
		d.Position = Position(strings.Index(q, name+".") + 1)
		if len(prepared) > 0 && prepared[0] {
			stage = "prepare"
		}
	}
	if kind == "schema" && strings.HasPrefix(q, "CREATE TABLE") {
		d.Position = Position(strings.Index(q, name+".") + 1)
	}
	s := ImplicitError(q, d)
	s.ErrorStage = stage
	run(s)
}

func RoleGrantError(run func(Step), q, code, message string) {
	s := ImplicitError(q, Diagnostic{Code: code, Message: message})
	s.ErrorStage = "execute"
	run(s)
}

func RoleGrantSyntax(run func(Step), q, token string, prepared bool) {
	s := ImplicitError(q, Diagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: Position(strings.LastIndex(q, token) + 1)})
	s.ErrorStage = "execute"
	if prepared {
		s.ErrorStage = "prepare"
	}
	run(s)
}

func RoleGrantEnvironment(t *testing.T, p *Pair, c *sql.Conn) (string, string) {
	t.Helper()
	var owner, database string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_user,current_database()").Scan(&owner, &database); err != nil {
		t.Fatal(err)
	}
	if owner == "" || database == "" {
		t.Fatal("empty environment identity", owner, database)
	}
	return owner, database
}

func RoleGrantNoticeExec(t *testing.T, p *Pair, conn *sql.Conn, recorder *Recorder, prepared bool, notices *[]Diagnostic, query string, want ...Diagnostic) {
	t.Helper()
	*notices = nil
	stmt, err := conn.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, "Prepare", err)
	}
	if len(*notices) != 0 {
		t.Fatalf("%s Prepare notices %#v", query, *notices)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal("close quiet description", err)
	}
	step := Exec(query, 0)
	step.Prepared = prepared
	RunStep(t, p.Ctx, conn, recorder, step)
	if len(*notices) != len(want) {
		t.Fatalf("%s notices %#v want %#v", query, *notices, want)
	}
	for i := range want {
		if (*notices)[i] != want[i] {
			t.Fatalf("%s notice %d got %#v want %#v", query, i, (*notices)[i], want[i])
		}
	}
}
