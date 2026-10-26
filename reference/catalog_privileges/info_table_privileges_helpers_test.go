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

var tpTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR"}
var tpPrivileges = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"}
var tpRoles = []string{"tp_o", "tp_r", "tp_d", "tp_e", "tp_p", "tp_m", "tp_g", "tp_x", "tp_y", "tp_power", "tp_renamed", "tp_xowner"}

const tpQuery = "SELECT * FROM information_schema.table_privileges WHERE table_name=$1 AND grantee=$2"

type tpCase struct {
	t               *testing.T
	p               *fixture.Pair
	b               fixture.Backend
	conn            *sql.Conn
	recorder        *fixture.Recorder
	owner, database string
}

func tpScenario(t *testing.T, body func(*tpCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, tpRoles)
	fixture.SchemaReferenceCleanup(t, "tp_private")
	fixture.SchemaReferenceCleanup(t, "tp_moved")
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
			c := &tpCase{t: t, p: p, b: b, conn: conn, recorder: recorder}
			c.owner, c.database = fixture.RoleGrantEnvironment(t, p, conn)
			body(c)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}
func (c *tpCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.recorder, s)
}
func (c *tpCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *tpCase) seed() {
	c.exec("CREATE ROLE tp_o", "CREATE ROLE tp_r", "CREATE ROLE tp_d", "CREATE ROLE tp_e", "CREATE ROLE tp_p", "CREATE ROLE tp_m", "CREATE ROLE tp_g", "CREATE ROLE tp_x", "CREATE ROLE tp_y", "CREATE ROLE tp_power SUPERUSER", "GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE TABLE tp_t(a integer)", "ALTER TABLE tp_t OWNER TO tp_o")
}
func (c *tpCase) actor(name string, body func()) {
	c.exec("SET SESSION AUTHORIZATION " + name)
	body()
	c.exec("RESET SESSION AUTHORIZATION")
}
func (c *tpCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
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
func (c *tpCase) peer() *sql.Conn {
	c.t.Helper()
	s, err := c.b.DB.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION"} {
			if _, err := s.ExecContext(c.p.Ctx, q); err != nil {
				c.t.Error(err)
			}
		}
		if err := s.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return s
}
func (c *tpCase) row(grantor, grantee, schema, table, privilege, option string) []any {
	hierarchy := "NO"
	if privilege == "SELECT" {
		hierarchy = "YES"
	}
	return []any{grantor, grantee, c.database, schema, table, privilege, option, hierarchy}
}
func (c *tpCase) r(privilege, option string) []any {
	return c.row("tp_o", "tp_r", "public", "tp_t", privilege, option)
}
func (c *tpCase) baseline(owner, table string) [][]any {
	var rows [][]any
	for _, privilege := range tpPrivileges {
		rows = append(rows, c.row(owner, owner, "public", table, privilege, "YES"))
	}
	return rows
}
func (c *tpCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, fixture.RTGColumns, tpTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { tpMetadata(t, rows, c.b.PG) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		want := reflect.TypeFor[string]()
		if c.b.PG && column < 5 {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("raw %d,%d=%T, want %v", row, column, value, want)
		}
		return false
	}
	return s
}
func (c *tpCase) check(e fixture.Executor, table, grantee string, want ...[]any) {
	c.t.Helper()
	s := c.step(tpQuery, want, table, grantee)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func tpMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, fixture.RTGColumns) {
		t.Fatal("columns", names)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	tpColumnTypes(t, types, pg)
	return types
}
func tpColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
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
			if i >= 6 {
				length = 3
			}
		}
		n, ok := typ.Nullable()
		l, lok := typ.Length()
		p, s, dok := typ.DecimalSize()
		if typ.Name() != fixture.RTGColumns[i] || typ.DatabaseTypeName() != tpTypes[i] || typ.ScanType() != scan || n != !pg || ok != !pg || l != length || lok != hasLength || p != 0 || s != 0 || dok {
			t.Fatalf("column %d: name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v length=%d/%t nullable=%t/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, ok, l, lok, p, s, dok, scan, length, hasLength, !pg, !pg)
		}
	}
}
func tpConsume(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	saved := tpMetadata(t, rows, pg)
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
	tpColumnTypes(t, saved, pg)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	tpColumnTypes(t, saved, pg)
}
