package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var ugColumns = []string{"grantor", "grantee", "udt_catalog", "udt_schema", "udt_name", "privilege_type", "is_grantable"}
var ugTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR"}
var ugRoles = []string{"ug_o", "ug_r", "ug_d", "ug_m", "ug_g", "ug_x", "ug_y", "ug_p", "ug_power"}

const ugQuery = "SELECT * FROM information_schema.role_udt_grants WHERE udt_schema=$1 AND udt_name=$2"

type ugCase struct {
	t               *testing.T
	p               *fixture.Pair
	b               fixture.Backend
	conn            *sql.Conn
	recorder        *fixture.Recorder
	owner, database string
}

func ugScenario(t *testing.T, body func(*ugCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, ugRoles)
	fixture.SchemaReferenceCleanup(t, "ug_private")
	fixture.SchemaReferenceCleanup(t, "ug_moved")
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
			c := &ugCase{t: t, p: p, b: b, conn: conn, recorder: recorder}
			c.owner, c.database = fixture.RoleGrantEnvironment(t, p, conn)
			body(c)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}
func (c *ugCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.recorder, s)
}
func (c *ugCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *ugCase) seed() {
	for _, role := range ugRoles {
		suffix := ""
		if role == "ug_power" {
			suffix = " SUPERUSER"
		}
		c.exec("CREATE ROLE " + role + suffix)
	}
	c.exec("GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE TYPE public.ug_t AS (a integer)", "ALTER TYPE public.ug_t OWNER TO ug_o")
}
func (c *ugCase) actor(role string, body func()) {
	c.exec("SET SESSION AUTHORIZATION " + role)
	body()
	c.exec("RESET SESSION AUTHORIZATION")
}
func (c *ugCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
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
func (c *ugCase) peer() *sql.Conn {
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
func (c *ugCase) row(grantor, grantee, schema, name, option string) []any {
	return []any{grantor, grantee, c.database, schema, name, "TYPE USAGE", option}
}
func (c *ugCase) origin(grantor, grantee, option string) []any {
	return c.row(grantor, grantee, "public", "ug_t", option)
}
func (c *ugCase) baseline(owner, schema, name string) [][]any {
	return [][]any{c.row(owner, owner, schema, name, "YES"), c.row(owner, "PUBLIC", schema, name, "NO")}
}
func (c *ugCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, ugColumns, ugTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ugMetadata(t, rows, c.b.PG) }
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
		values, targets := make([]sql.NullString, 7), make([]any, 7)
		for i := range targets {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		pointers := make([]*string, 7)
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
func (c *ugCase) check(e fixture.Executor, schema, name string, want ...[]any) {
	c.t.Helper()
	s := c.step(ugQuery, want, schema, name)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func ugMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, ugColumns) {
		t.Fatal("columns", names)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	ugColumnTypes(t, types, pg)
	return types
}
func ugColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 7 {
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
			if i == 6 {
				length = 3
			}
		}
		n, nok := typ.Nullable()
		l, lok := typ.Length()
		p, s, dok := typ.DecimalSize()
		if typ.Name() != ugColumns[i] || typ.DatabaseTypeName() != ugTypes[i] || typ.ScanType() != scan || n != !pg || nok != !pg || l != length || lok != hasLength || p != 0 || s != 0 || dok {
			t.Fatalf("column %d: name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v nullable=%t/%t length=%d/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, nok, l, lok, p, s, dok, scan, !pg, !pg, length, hasLength)
		}
	}
}
func (c *ugCase) acl(kind string) {
	c.t.Helper()
	q := "SELECT typacl IS NULL AS absent,COALESCE(cardinality(typacl),0)::bigint AS entries FROM pg_type WHERE oid='public.ug_t'::regtype"
	want := [][]any{{kind == "absent", int64(0)}}
	if kind == "nonempty" {
		q = "SELECT typacl IS NULL AS absent,cardinality(typacl)>0 AS nonempty FROM pg_type WHERE oid='public.ug_t'::regtype"
		c.run(c.conn, fixture.Select(q, []string{"absent", "nonempty"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		return
	}
	c.run(c.conn, fixture.Select(q, []string{"absent", "entries"}, []string{"BOOL", "INT8"}, want))
}
