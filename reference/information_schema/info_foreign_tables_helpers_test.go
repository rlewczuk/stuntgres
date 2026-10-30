package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const iftProjection = "foreign_table_catalog,foreign_table_schema,foreign_table_name,foreign_server_catalog,foreign_server_name"
const iftQuery = "SELECT " + iftProjection + " FROM information_schema.foreign_tables WHERE foreign_table_name=$1 ORDER BY foreign_table_schema,foreign_table_name"

var iftColumns = []string{"foreign_table_catalog", "foreign_table_schema", "foreign_table_name", "foreign_server_catalog", "foreign_server_name"}
var iftTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME"}
var iftRoles = []string{"ift_owner", "ift_inherit", "ift_set", "ift_reader", "ift_denied", "ift_newowner", "ift_super", "ift_column"}

type iftCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func iftScenario(t *testing.T, body func(*iftCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, iftRoles)
	fixture.SchemaReferenceCleanup(t, "ift_scope")
	fixture.SchemaReferenceCleanup(t, "ift_moved")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS ift_scope CASCADE", "DROP SCHEMA IF EXISTS ift_moved CASCADE", "DROP SERVER IF EXISTS ift_server CASCADE", "DROP SERVER IF EXISTS ift_second_server CASCADE", "DROP SERVER IF EXISTS ift_renamed_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS ift_wrapper CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS ift_second CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS ift_empty CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS ift_renamed CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			c := &iftCase{t: t, p: p, b: backend, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *iftCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *iftCase) exec(qs ...string) {
	c.t.Helper()
	for _, q := range qs {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *iftCase) seed() {
	c.exec("CREATE FOREIGN DATA WRAPPER ift_wrapper NO HANDLER NO VALIDATOR OPTIONS(wrapper_decoy 'wrapper-only')", "CREATE SERVER ift_server FOREIGN DATA WRAPPER ift_wrapper OPTIONS(server_decoy 'server-only')", "CREATE SCHEMA ift_scope", "CREATE FOREIGN TABLE ift_scope.ift_table(a integer OPTIONS(column_decoy 'column-only'),b integer) SERVER ift_server OPTIONS(empty '',password 'visible=value',remote 'é=🙂',quoted 'a''b\\c  ')", "CREATE USER MAPPING FOR PUBLIC SERVER ift_server OPTIONS(mapping_decoy 'mapping-only')")
}
func (c *iftCase) row(schema, table, server string) []any {
	return []any{c.database, schema, table, c.database, server}
}
func (c *iftCase) seedRows() [][]any {
	return [][]any{c.row("ift_scope", "ift_table", "ift_server")}
}
func (c *iftCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
	c.t.Helper()
	s, err := e.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		if err := s.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: e, Statement: s, SQL: q}
}
func iftMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(cols) != 5 {
		t.Fatalf("columns=%d want 5", len(cols))
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		wl, wok := int64(0), false
		p, s, d := col.DecimalSize()
		n, nok := col.Nullable()
		if col.Name() != iftColumns[i] || col.DatabaseTypeName() != iftTypes[i] || col.ScanType() != scan || length != wl || lengthOK != wok || p != 0 || s != 0 || d || n != !pg || nok != !pg {
			t.Fatalf("column %d: %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, p, s, d, n, nok)
		}
	}
}
func (c *iftCase) step(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, iftColumns, iftTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cols, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		iftMetadata(t, cols, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if want[row][column] == nil {
			if v != nil {
				t.Fatalf("NULL=%T(%v)", v, v)
			}
			return true
		}
		text, ok := v.(string)
		if c.b.PG {
			b, bok := v.([]byte)
			text, ok = string(b), bok
		}
		if !ok || text != want[row][column] {
			t.Fatalf("raw [%d,%d]=%T(%v), want %v", row, column, v, v, want[row][column])
		}
		return true
	}
	return s
}
func (c *iftCase) check(e fixture.Executor, schema any, want [][]any) {
	c.t.Helper()
	c.run(e, c.step(iftQuery, want, schema))
}
func (c *iftCase) routes(q string, want [][]any, args ...any) {
	c.t.Helper()
	s := c.step(q, want, args...)
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
	held := c.hold(c.conn, q)
	c.run(held, s)
	if err := held.Statement.Close(); err != nil {
		c.t.Fatal(err)
	}
}
func (c *iftCase) consume(r *sql.Rows, want [][]any) {
	c.t.Helper()
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	iftMetadata(c.t, cols, c.b.PG)
	s := c.step("", want)
	n := 0
	for r.Next() {
		if n >= len(want) {
			c.t.Fatal("extra row")
		}
		v := make([]any, 5)
		dst := make([]any, 5)
		for i := range v {
			dst[i] = &v[i]
		}
		if err := r.Scan(dst...); err != nil {
			c.t.Fatal(err)
		}
		for i := range v {
			s.CheckRaw(c.t, i, n, v[i])
		}
		n++
	}
	if r.Err() != nil || n != len(want) {
		c.t.Fatalf("rows=%d want=%d error=%v", n, len(want), r.Err())
	}
	iftMetadata(c.t, cols, c.b.PG)
	if err := r.Close(); err != nil {
		c.t.Fatal(err)
	}
	iftMetadata(c.t, cols, c.b.PG)
}
