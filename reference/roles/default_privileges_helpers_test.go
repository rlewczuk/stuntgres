package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var dpRoles = []string{"dp_reader", "dp_owner", "dp_member", "dp_set", "dp_other", "dp_future"}
var dpSchemas = []string{"dp_s", "dp_t", "dp_owner_ns", "dp_member_ns", "dp_new_ns", "dp_renamed"}

// Both utility and consumer routes share independent expectations. Cleanup is
// registered before mutation, including defaults for providers without creation.
func dpScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step), bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, append(append([]string{}, dpRoles...), "dp_Quoted"))
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer func() {
						// The oracle persists role-global defaults across public-schema resets.
						// Native engines are isolated; no unsupported native cleanup is invented.
						if !b.PG {
							return
						}
						if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
							t.Error(err)
						}
						if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
							t.Error(err)
						}
						for _, s := range dpSchemas {
							if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+s+" CASCADE"); err != nil {
								t.Error(err)
							}
						}
						var bootstrap string
						if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&bootstrap); err != nil {
							t.Error(err)
							return
						}
						owners := append([]string{bootstrap, "dp_Quoted"}, dpRoles...)
						recipients := append([]string{"PUBLIC"}, dpRoles...)
						existing := make(map[string]bool, len(owners))
						for _, owner := range owners {
							var exists bool
							if err := c.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", owner).Scan(&exists); err != nil {
								t.Error(err)
								continue
							}
							existing[owner] = exists
						}
						for _, owner := range owners {
							if !existing[owner] {
								continue
							}
							owner = `"` + owner + `"`
							for _, class := range []string{"TABLES", "SEQUENCES", "FUNCTIONS", "TYPES", "SCHEMAS", "LARGE OBJECTS"} {
								for _, recipient := range recipients {
									if recipient != "PUBLIC" && !existing[recipient] {
										continue
									}
									if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" REVOKE ALL ON "+class+" FROM "+recipient+" CASCADE"); err != nil {
										t.Error(err)
									}
								}
								if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" GRANT ALL ON "+class+" TO "+owner); err != nil {
									t.Error(err)
								}
								if class == "FUNCTIONS" || class == "TYPES" {
									if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" GRANT ALL ON "+class+" TO PUBLIC"); err != nil {
										t.Error(err)
									}
								}
							}
						}
					}()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
							t.FailNow()
						}
					}
					if b.PG {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
					}
					for _, role := range dpRoles {
						run(fixture.Exec("CREATE ROLE "+role, 0))
					}
					body(t, p, b, c, run, prepared)
				})
			}
		})
	}
}

func dpActor(run func(fixture.Step), role string, body func()) {
	run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
	body()
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}
func dpSchema(run func(fixture.Step), name, owner string) {
	run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" GRANT USAGE ON SCHEMAS TO dp_reader", 0))
	run(fixture.Exec("CREATE SCHEMA "+name+" AUTHORIZATION "+owner, 0))
}
func dpTable(run func(fixture.Step), name string, value int) {
	run(fixture.Exec("CREATE TABLE "+name+"(x INT)", 0))
	run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%d)", name, value), 1))
}
func dpRead(run func(fixture.Step), name string, values ...int64) {
	rows := make([][]any, 0, len(values))
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	run(fixture.Select("SELECT x FROM "+name+" ORDER BY x", []string{"x"}, []string{"INT4"}, rows))
}
func dpDenied(run func(fixture.Step), q, kind, name string, prepared ...bool) {
	d := fixture.Diagnostic{Code: "42501", Message: "permission denied for " + kind + " " + name}
	stage := "execute"
	if kind == "schema" && strings.HasPrefix(q, "SELECT") {
		d.Position = fixture.Position(strings.Index(q, name+".") + 1)
		if len(prepared) > 0 && prepared[0] {
			stage = "prepare"
		}
	}
	if kind == "schema" && strings.HasPrefix(q, "CREATE TABLE") {
		d.Position = fixture.Position(strings.Index(q, name+".") + 1)
	}
	s := fixture.ImplicitError(q, d)
	s.ErrorStage = stage
	run(s)
}
func dpError(run func(fixture.Step), q, code, message string) {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.ErrorStage = "execute"
	run(s)
}
func dpSyntax(run func(fixture.Step), q, token string, prepared bool) {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: fixture.Position(strings.LastIndex(q, token) + 1)})
	s.ErrorStage = "execute"
	if prepared {
		s.ErrorStage = "prepare"
	}
	run(s)
}
func dpDependency(run func(fixture.Step), role, class, owner, schema string, creator bool) {
	description := "default privileges on new " + class + " belonging to role " + owner
	if schema != "" {
		description += " in schema " + schema
	}
	prefix := "privileges for "
	if creator {
		prefix = "owner of "
	}
	s := fixture.ImplicitError("DROP ROLE "+role, fixture.Diagnostic{Code: "2BP01", Message: `role "` + role + `" cannot be dropped because some objects depend on it`, Detail: prefix + description})
	s.ErrorStage = "execute"
	run(s)
}
