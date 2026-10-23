package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var rcgColumns = []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "column_name", "privilege_type", "is_grantable"}
var rcgTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR"}
var rcgRoles = []string{"rcg_o", "rcg_r", "rcg_d", "rcg_e", "rcg_p", "rcg_m", "rcg_g", "rcg_x", "rcg_y", "rcg_power", "rcg_renamed"}

const rcgQuery = "SELECT * FROM information_schema.role_column_grants WHERE table_name=$1 AND grantee=$2"

type rcgCase struct {
	t               *testing.T
	p               *fixture.Pair
	b               fixture.Backend
	conn            *sql.Conn
	recorder        *fixture.Recorder
	owner, database string
}

func rcgScenario(t *testing.T, body func(*rcgCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, rcgRoles)
	fixture.SchemaReferenceCleanup(t, "rcg_private")
	fixture.SchemaReferenceCleanup(t, "rcg_moved")
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
			c := &rcgCase{t: t, p: p, b: b, conn: conn, recorder: recorder}
			c.owner, c.database = fixture.RoleGrantEnvironment(t, p, conn)
			body(c)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}
func (c *rcgCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.recorder, s)
}
func (c *rcgCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *rcgCase) seed() {
	c.exec("CREATE ROLE rcg_o", "CREATE ROLE rcg_r", "CREATE ROLE rcg_d", "CREATE ROLE rcg_e", "CREATE ROLE rcg_p", "CREATE ROLE rcg_m", "CREATE ROLE rcg_g", "CREATE ROLE rcg_x", "CREATE ROLE rcg_y", "CREATE ROLE rcg_power SUPERUSER", "GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE TABLE rcg_t(a integer,b integer)", "ALTER TABLE rcg_t OWNER TO rcg_o")
}
func (c *rcgCase) actor(name string, body func()) {
	c.exec("SET SESSION AUTHORIZATION " + name)
	body()
	c.exec("RESET SESSION AUTHORIZATION")
}
func (c *rcgCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
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
func (c *rcgCase) peer() *sql.Conn {
	c.t.Helper()
	s, err := c.b.DB.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		s.ExecContext(c.p.Ctx, "RESET ROLE")
		s.ExecContext(c.p.Ctx, "RESET SESSION AUTHORIZATION")
		if err := s.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return s
}
func (c *rcgCase) row(grantor, grantee, schema, table, column, privilege, option string) []any {
	return []any{grantor, grantee, c.database, schema, table, column, privilege, option}
}
func (c *rcgCase) r(column, privilege, option string) []any {
	return c.row("rcg_o", "rcg_r", "public", "rcg_t", column, privilege, option)
}
func (c *rcgCase) baseline(owner, table string, columns ...string) [][]any {
	var rows [][]any
	for _, column := range columns {
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			rows = append(rows, c.row(owner, owner, "public", table, column, privilege, "YES"))
		}
	}
	return rows
}
func (c *rcgCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, rcgColumns, rcgTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rcgMetadata(t, rows, c.b.PG) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			t.Fatalf("unexpected NULL at %d,%d", row, column)
		}
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
func (c *rcgCase) check(e fixture.Executor, table, grantee string, want ...[]any) {
	c.t.Helper()
	s := c.step(rcgQuery, want, table, grantee)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func rcgMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, rcgColumns) {
		t.Fatal("columns", names)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	rcgColumnTypes(t, types, pg)
	return types
}
func rcgColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 8 {
		t.Fatal("column count", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		length, hasLength := int64(0), false
		if i < 6 && pg {
			scan = reflect.TypeFor[any]()
		}
		if i >= 6 {
			length, hasLength = math.MaxInt64, true
			if pg {
				length = -5
			}
			if i == 7 {
				length = 3
			}
		}
		n, ok := typ.Nullable()
		l, lok := typ.Length()
		p, s, dok := typ.DecimalSize()
		if typ.Name() != rcgColumns[i] || typ.DatabaseTypeName() != rcgTypes[i] || typ.ScanType() != scan || n != !pg || ok != !pg || l != length || lok != hasLength || p != 0 || s != 0 || dok {
			t.Fatalf("column %d: name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v length=%d/%t nullable=%t/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, ok, l, lok, p, s, dok, scan, length, hasLength, !pg, !pg)
		}
	}
}
func rcgConsume(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	saved := rcgMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		values, targets := make([]any, 8), make([]any, 8)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	key := func(r []any) string { return fmt.Sprint(r) }
	sort.Slice(got, func(i, j int) bool { return key(got[i]) < key(got[j]) })
	expected := append([][]any(nil), want...)
	sort.Slice(expected, func(i, j int) bool { return key(expected[i]) < key(expected[j]) })
	if len(got) != len(expected) || len(got) > 0 && !reflect.DeepEqual(got, expected) {
		t.Fatalf("rows=%v want=%v", got, expected)
	}
	rcgColumnTypes(t, saved, pg)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	rcgColumnTypes(t, saved, pg)
}
