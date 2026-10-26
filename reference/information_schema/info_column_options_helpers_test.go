package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const copProjection = "table_catalog,table_schema,table_name,column_name,option_name,option_value"
const copQuery = "SELECT " + copProjection + " FROM information_schema.column_options WHERE table_schema=$1 ORDER BY table_name,column_name,option_name"

var copColumns = fixture.COColumns
var copTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR"}
var copRoles = []string{"cop_owner", "cop_inherit", "cop_set", "cop_reader", "cop_denied", "cop_newowner", "cop_group"}

type copCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func copScenario(t *testing.T, body func(*copCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, copRoles)
	fixture.SchemaReferenceCleanup(t, "cop_scope", "cop_moved")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS cop_scope CASCADE", "DROP SCHEMA IF EXISTS cop_moved CASCADE", "DROP SERVER IF EXISTS cop_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS cop_wrapper CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			c := &copCase{t: t, p: p, b: backend, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *copCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *copCase) exec(qs ...string) {
	c.t.Helper()
	for _, q := range qs {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *copCase) seed() {
	c.exec("CREATE FOREIGN DATA WRAPPER cop_wrapper NO HANDLER NO VALIDATOR OPTIONS(remote 'wrapper-only')", "CREATE SERVER cop_server FOREIGN DATA WRAPPER cop_wrapper OPTIONS(remote 'server-only')", "CREATE SCHEMA cop_scope", "CREATE FOREIGN TABLE cop_scope.options(id integer OPTIONS(remote 'key', password 'visible=owner'),label text OPTIONS(empty '',remote 'é=🙂',quoted 'a''b')) SERVER cop_server OPTIONS(remote 'table-only')")
}
func (c *copCase) row(table, column, option, value string) []any {
	return []any{c.database, "cop_scope", table, column, option, value}
}
func (c *copCase) seedRows() [][]any {
	return [][]any{c.row("options", "id", "password", "visible=owner"), c.row("options", "id", "remote", "key"), c.row("options", "label", "empty", ""), c.row("options", "label", "quoted", "a'b"), c.row("options", "label", "remote", "é=🙂")}
}
func (c *copCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
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
func copMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(cols) != 6 {
		t.Fatalf("columns=%d want 6", len(cols))
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if pg && i < 5 {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		wl, wok := int64(0), false
		if i == 5 {
			wl, wok = math.MaxInt64, true
			if pg {
				wl = -5
			}
		}
		p, s, d := col.DecimalSize()
		n, nok := col.Nullable()
		if col.Name() != copColumns[i] || col.DatabaseTypeName() != copTypes[i] || col.ScanType() != scan || length != wl || lengthOK != wok || p != 0 || s != 0 || d || n != !pg || nok != !pg {
			t.Fatalf("column %d: %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, p, s, d, n, nok)
		}
	}
}
func (c *copCase) step(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, copColumns, copTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cols, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		copMetadata(t, cols, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if want[row][column] == nil {
			if v != nil {
				t.Fatalf("NULL=%T(%v)", v, v)
			}
			return true
		}
		text, ok := v.(string)
		if c.b.PG && column < 5 {
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
func (c *copCase) check(e fixture.Executor, schema any, want [][]any) {
	c.t.Helper()
	c.run(e, c.step(copQuery, want, schema))
}
func (c *copCase) routes(q string, want [][]any, args ...any) {
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
func (c *copCase) consume(r *sql.Rows, want [][]any) {
	c.t.Helper()
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	copMetadata(c.t, cols, c.b.PG)
	s := c.step("", want)
	n := 0
	for r.Next() {
		if n >= len(want) {
			c.t.Fatal("extra row")
		}
		v := make([]any, 6)
		dst := make([]any, 6)
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
	copMetadata(c.t, cols, c.b.PG)
	if err := r.Close(); err != nil {
		c.t.Fatal(err)
	}
	copMetadata(c.t, cols, c.b.PG)
}
