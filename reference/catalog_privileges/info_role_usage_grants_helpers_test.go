package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var rugColumns = []string{"grantor", "grantee", "object_catalog", "object_schema", "object_name", "object_type", "privilege_type", "is_grantable"}
var rugTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR"}
var rugRoles = []string{"rug_o", "rug_r", "rug_d", "rug_m", "rug_g", "rug_x", "rug_y", "rug_p", "rug_power"}

const rugQuery = "SELECT * FROM information_schema.role_usage_grants WHERE object_schema=$1 AND object_name=$2"

type rugCase struct {
	t               *testing.T
	p               *fixture.Pair
	b               fixture.Backend
	conn            *sql.Conn
	recorder        *fixture.Recorder
	owner, database string
}

func rugScenario(t *testing.T, body func(*rugCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, rugRoles)
	fixture.SchemaReferenceCleanup(t, "rug_private")
	fixture.SchemaReferenceCleanup(t, "rug_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
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
			c := &rugCase{t: t, p: p, b: b, conn: conn, recorder: recorder}
			c.owner, c.database = fixture.RoleGrantEnvironment(t, p, conn)
			body(c)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}
func (c *rugCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.recorder, s)
}
func (c *rugCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *rugCase) seed() {
	for _, role := range rugRoles {
		suffix := ""
		if role == "rug_power" {
			suffix = " SUPERUSER"
		}
		c.exec("CREATE ROLE " + role + suffix)
	}
	c.exec("GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE DOMAIN public.rug_t AS integer", "ALTER TYPE public.rug_t OWNER TO rug_o")
}
func (c *rugCase) actor(role string, body func()) {
	c.exec("SET SESSION AUTHORIZATION " + role)
	body()
	c.exec("RESET SESSION AUTHORIZATION")
}
func (c *rugCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := e.PrepareContext(c.p.Ctx, query)
	if err != nil {
		c.t.Fatal(query, err)
	}
	c.t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: e, Statement: stmt, SQL: query}
}
func (c *rugCase) peer() *sql.Conn {
	c.t.Helper()
	conn, err := c.b.DB.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
			if _, err := conn.ExecContext(c.p.Ctx, q); err != nil {
				c.t.Error(err)
			}
		}
		if err := conn.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return conn
}
func (c *rugCase) row(grantor, grantee, schema, name, option string) []any {
	return []any{grantor, grantee, c.database, schema, name, "DOMAIN", "USAGE", option}
}
func (c *rugCase) origin(grantor, grantee, option string) []any {
	return c.row(grantor, grantee, "public", "rug_t", option)
}
func (c *rugCase) baseline(owner, schema, name string) [][]any {
	return [][]any{c.row(owner, owner, schema, name, "YES"), c.row(owner, "PUBLIC", schema, name, "NO")}
}
func (c *rugCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, rugColumns, rugTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rugMetadata(t, rows, c.b.PG) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		want := reflect.TypeFor[string]()
		if c.b.PG && column < 5 {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("raw %d,%d=%T want %v", row, column, value, want)
		}
		return false
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		values, targets := make([]sql.NullString, 8), make([]any, 8)
		for i := range targets {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		pointers := make([]*string, 8)
		for i := range targets {
			targets[i] = &pointers[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			if !values[i].Valid || pointers[i] == nil || *pointers[i] != values[i].String {
				t.Fatalf("scan %d: %+v %v", i, values[i], pointers[i])
			}
		}
	}
	return s
}
func (c *rugCase) check(e fixture.Executor, schema, name string, want ...[]any) {
	c.t.Helper()
	s := c.step(rugQuery, want, schema, name)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func rugMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, rugColumns) {
		t.Fatal("columns", names)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	rugColumnTypes(t, types, pg)
	return types
}
func rugColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 8 {
		t.Fatal("column count", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		length, hasLength := int64(0), false
		if i < 5 && pg {
			scan = reflect.TypeFor[any]()
		}
		if i >= 5 {
			length, hasLength = math.MaxInt64, true
			if pg {
				length = -5
			}
			if i == 7 {
				length = 3
			}
		}
		n, nok := typ.Nullable()
		l, lok := typ.Length()
		p, s, dok := typ.DecimalSize()
		if typ.Name() != rugColumns[i] || typ.DatabaseTypeName() != rugTypes[i] || typ.ScanType() != scan || n != !pg || nok != !pg || l != length || lok != hasLength || p != 0 || s != 0 || dok {
			t.Fatalf("column %d: name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v nullable=%t/%t length=%d/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, nok, l, lok, p, s, dok, scan, !pg, !pg, length, hasLength)
		}
	}
}
func (c *rugCase) acl(kind string) {
	c.t.Helper()
	q := "SELECT typacl IS NULL AS absent,COALESCE(cardinality(typacl),0)::bigint AS entries FROM pg_type WHERE oid='public.rug_t'::regtype"
	want := [][]any{{kind == "absent", int64(0)}}
	if kind == "nonempty" {
		q = "SELECT typacl IS NULL AS absent,cardinality(typacl)>0 AS nonempty FROM pg_type WHERE oid='public.rug_t'::regtype"
		c.run(c.conn, fixture.Select(q, []string{"absent", "nonempty"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		return
	}
	c.run(c.conn, fixture.Select(q, []string{"absent", "entries"}, []string{"BOOL", "INT8"}, want))
}
