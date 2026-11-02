package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var cpColumns = []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "column_name", "privilege_type", "is_grantable"}
var cpTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR"}
var cpRoles = []string{"cp_o", "cp_r", "cp_d", "cp_e", "cp_g", "cp_m", "cp_x", "cp_y", "cp_p", "cp_power", "PUBLIC"}

const cpQuery = "SELECT * FROM information_schema.column_privileges WHERE table_name=$1 AND grantee=$2"
const cpRoleQuery = "SELECT * FROM information_schema.role_column_grants WHERE table_name=$1 AND grantee=$2"

type cpCase struct {
	t               *testing.T
	p               *fixture.Pair
	b               fixture.Backend
	conn            *sql.Conn
	recorder        *fixture.Recorder
	owner, database string
}

func cpScenario(t *testing.T, body func(*cpCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, cpRoles)
	fixture.SchemaReferenceCleanup(t, "cp_scope")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			t.Cleanup(func() {
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			c := &cpCase{t: t, p: p, b: backend, conn: conn, recorder: recorder}
			c.owner, c.database = fixture.RoleGrantEnvironment(t, p, conn)
			body(c)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}

func (c *cpCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.recorder, s)
}
func (c *cpCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *cpCase) seed() {
	c.exec("CREATE ROLE cp_o", "CREATE ROLE cp_r", "CREATE ROLE cp_d", "CREATE ROLE cp_e", "CREATE ROLE cp_g", "CREATE ROLE cp_m", "CREATE ROLE cp_x", "CREATE ROLE cp_y", "CREATE ROLE cp_p", "CREATE ROLE cp_power SUPERUSER", "GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE TABLE cp_t(a integer,b integer)", "ALTER TABLE cp_t OWNER TO cp_o")
}
func (c *cpCase) actor(role string, body func()) {
	c.exec("SET SESSION AUTHORIZATION " + role)
	body()
	c.exec("RESET SESSION AUTHORIZATION")
}
func (c *cpCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
	c.t.Helper()
	s, err := e.PrepareContext(c.p.Ctx, query)
	if err != nil {
		c.t.Fatal(query, err)
	}
	c.t.Cleanup(func() {
		if err := s.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: e, Statement: s, SQL: query}
}
func (c *cpCase) peer() *sql.Conn {
	c.t.Helper()
	conn, err := c.b.DB.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		conn.ExecContext(c.p.Ctx, "RESET ROLE")
		conn.ExecContext(c.p.Ctx, "RESET SESSION AUTHORIZATION")
		if err := conn.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return conn
}
func (c *cpCase) row(grantor, grantee, schema, table, column, privilege, option string) []any {
	return []any{grantor, grantee, c.database, schema, table, column, privilege, option}
}
func (c *cpCase) r(column, privilege, option string) []any {
	return c.row("cp_o", "cp_r", "public", "cp_t", column, privilege, option)
}
func (c *cpCase) baseline(owner, table string, columns ...string) [][]any {
	var want [][]any
	for _, column := range columns {
		for _, privilege := range []string{"INSERT", "SELECT", "UPDATE", "REFERENCES"} {
			want = append(want, c.row(owner, owner, "public", table, column, privilege, "YES"))
		}
	}
	return want
}
func (c *cpCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, cpColumns, cpTypes, want, args...)
	s.Bag = true
	// These two released views have the identical domain/adapter shape. Reuse
	// the existing independent metadata assertion, not production descriptors.
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rcgMetadata(t, rows, c.b.PG) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		want := reflect.TypeFor[string]()
		if c.b.PG && column < 6 {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("raw %d,%d=%T, want %v", row, column, value, want)
		}
		return false
	}
	return s
}
func (c *cpCase) check(e fixture.Executor, table, grantee string, want ...[]any) {
	c.t.Helper()
	s := c.step(cpQuery, want, table, grantee)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func (c *cpCase) sibling(table, grantee string, want ...[]any) {
	c.t.Helper()
	s := c.step(cpRoleQuery, want, table, grantee)
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
}
