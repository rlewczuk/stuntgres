package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

var rrgColumns = []string{"grantor", "grantee", "specific_catalog", "specific_schema", "specific_name", "routine_catalog", "routine_schema", "routine_name", "privilege_type", "is_grantable"}
var rrgTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR"}
var rrgRoles = []string{"rrg_o", "rrg_r", "rrg_d", "rrg_p", "rrg_m", "rrg_g", "rrg_x", "rrg_y", "rrg_power"}
var rrgProjection = strings.Join(rrgColumns, ",")

const rrgQuery = "SELECT * FROM information_schema.role_routine_grants WHERE routine_schema=$1 AND routine_name=$2"

type rrgCase struct {
	t               *testing.T
	p               *fixture.Pair
	b               fixture.Backend
	conn            *sql.Conn
	recorder        *fixture.Recorder
	owner, database string
}

func rrgScenario(t *testing.T, body func(*rrgCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, rrgRoles)
	fixture.SchemaReferenceCleanup(t, "rrg_private")
	fixture.SchemaReferenceCleanup(t, "rrg_moved")
	fixture.SchemaReferenceCleanup(t, "rrg_ext")
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
			c := &rrgCase{t: t, p: p, b: b, conn: conn, recorder: recorder}
			c.owner, c.database = fixture.RoleGrantEnvironment(t, p, conn)
			body(c)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}
func (c *rrgCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.recorder, s)
}
func (c *rrgCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *rrgCase) seed() {
	c.exec("CREATE ROLE rrg_o", "CREATE ROLE rrg_r", "CREATE ROLE rrg_d", "CREATE ROLE rrg_p", "CREATE ROLE rrg_m", "CREATE ROLE rrg_g", "CREATE ROLE rrg_x", "CREATE ROLE rrg_y", "CREATE ROLE rrg_power SUPERUSER", "GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE FUNCTION public.rrg_f() RETURNS integer RETURN 7", "ALTER FUNCTION public.rrg_f() OWNER TO rrg_o")
}

// Discover identity from pg_proc, never from the view under test. regprocedure
// resolves overloads independently on each backend; OIDs are not parity values.
func (c *rrgCase) identity(signature string) (schema, name, owner, specific string, oid int64) {
	c.t.Helper()
	err := c.conn.QueryRowContext(c.p.Ctx, "SELECT n.nspname,p.proname,pg_get_userbyid(p.proowner),p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1::regprocedure::oid", signature).Scan(&schema, &name, &owner, &oid)
	if err != nil {
		c.t.Fatal(signature, err)
	}
	suffix := "_" + strconv.FormatInt(oid, 10)
	prefix := name
	for len(prefix)+len(suffix) > 63 {
		_, size := utf8.DecodeLastRuneInString(prefix)
		prefix = prefix[:len(prefix)-size]
	}
	return schema, name, owner, prefix + suffix, oid
}
func (c *rrgCase) actor(name string, body func()) {
	c.exec("SET SESSION AUTHORIZATION " + name)
	body()
	c.exec("RESET SESSION AUTHORIZATION")
}
func (c *rrgCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
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
func (c *rrgCase) peer() *sql.Conn {
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
func (c *rrgCase) row(grantor, grantee, schema, name, specific, option string) []any {
	return []any{grantor, grantee, c.database, schema, specific, c.database, schema, name, "EXECUTE", option}
}
func (c *rrgCase) baseline(signature string) [][]any {
	schema, name, owner, specific, _ := c.identity(signature)
	return [][]any{c.row(owner, owner, schema, name, specific, "YES"), c.row(owner, "PUBLIC", schema, name, specific, "NO")}
}
func (c *rrgCase) origin(grantor, grantee, option string) []any {
	schema, name, _, specific, _ := c.identity("public.rrg_f()")
	return c.row(grantor, grantee, schema, name, specific, option)
}
func (c *rrgCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, rrgColumns, rrgTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rrgMetadata(t, rows, c.b.PG) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		want := reflect.TypeFor[string]()
		if c.b.PG && column < 8 {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("raw %d,%d=%T, want %v", row, column, value, want)
		}
		return false
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		values, nullable, pointers := make([]any, 10), make([]sql.NullString, 10), make([]*string, 10)
		targets := make([]any, 10)
		for i := range targets {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range targets {
			targets[i] = &nullable[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range targets {
			targets[i] = &pointers[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			value, ok := fixture.Normalize(values[i]).(string)
			if !ok || !nullable[i].Valid || nullable[i].String != value || pointers[i] == nil || *pointers[i] != value {
				t.Fatalf("nonnull scan %d: raw=%v nullable=%+v pointer=%v", i, values[i], nullable[i], pointers[i])
			}
		}
	}
	return s
}
func (c *rrgCase) check(e fixture.Executor, schema, name string, want ...[]any) {
	c.t.Helper()
	s := c.step(rrgQuery, want, schema, name)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func rrgMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, rrgColumns) {
		t.Fatal("columns", names)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	rrgColumnTypes(t, types, pg)
	return types
}
func rrgColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 10 {
		t.Fatal("column count", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		length, hasLength := int64(0), false
		if i < 8 && pg {
			scan = reflect.TypeFor[any]()
		}
		if i >= 8 {
			length, hasLength = math.MaxInt64, true
			if pg {
				length = -5
			}
			if i >= 9 {
				length = 3
			}
		}
		n, ok := typ.Nullable()
		l, lok := typ.Length()
		p, s, dok := typ.DecimalSize()
		if typ.Name() != rrgColumns[i] || typ.DatabaseTypeName() != rrgTypes[i] || typ.ScanType() != scan || n != !pg || ok != !pg || l != length || lok != hasLength || p != 0 || s != 0 || dok {
			t.Fatalf("column %d: name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v length=%d/%t nullable=%t/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, ok, l, lok, p, s, dok, scan, length, hasLength, !pg, !pg)
		}
	}
}
func rrgConsume(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	saved := rrgMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		values, targets := make([]any, 10), make([]any, 10)
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
	rrgColumnTypes(t, saved, pg)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	rrgColumnTypes(t, saved, pg)
}
