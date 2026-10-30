package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const fdwoProjection = "foreign_data_wrapper_catalog,foreign_data_wrapper_name,option_name,option_value"
const fdwoQuery = "SELECT " + fdwoProjection + " FROM information_schema.foreign_data_wrapper_options WHERE foreign_data_wrapper_name=$1 ORDER BY foreign_data_wrapper_name,option_name"

var fdwoColumns = fixture.FDWOColumns
var fdwoTypes = []string{"NAME", "NAME", "NAME", "VARCHAR"}
var fdwoRoles = []string{"fdwo_owner", "fdwo_inherit", "fdwo_set", "fdwo_reader", "fdwo_denied", "fdwo_newowner", "fdwo_group"}

type fdwoCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func fdwoScenario(t *testing.T, body func(*fdwoCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, fdwoRoles)
	fixture.SchemaReferenceCleanup(t, "fdwo_scope")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS fdwo_scope CASCADE", "DROP SERVER IF EXISTS fdwo_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwo_wrapper CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwo_second CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwo_empty CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwo_renamed CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			c := &fdwoCase{t: t, p: p, b: backend, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *fdwoCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *fdwoCase) exec(qs ...string) {
	c.t.Helper()
	for _, q := range qs {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *fdwoCase) seed() {
	c.exec("CREATE FOREIGN DATA WRAPPER fdwo_wrapper NO HANDLER NO VALIDATOR OPTIONS(remote 'é=🙂',password 'private=value',empty '')")
}
func (c *fdwoCase) row(wrapper, option, value string) []any {
	return []any{c.database, wrapper, option, value}
}
func (c *fdwoCase) seedRows() [][]any {
	return [][]any{c.row("fdwo_wrapper", "empty", ""), c.row("fdwo_wrapper", "password", "private=value"), c.row("fdwo_wrapper", "remote", "é=🙂")}
}
func (c *fdwoCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
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
func fdwoMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(cols) != 4 {
		t.Fatalf("columns=%d want 4", len(cols))
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if pg && i < 3 {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		wl, wok := int64(0), false
		if i == 3 {
			wl, wok = math.MaxInt64, true
			if pg {
				wl = -5
			}
		}
		p, s, d := col.DecimalSize()
		n, nok := col.Nullable()
		if col.Name() != fdwoColumns[i] || col.DatabaseTypeName() != fdwoTypes[i] || col.ScanType() != scan || length != wl || lengthOK != wok || p != 0 || s != 0 || d || n != !pg || nok != !pg {
			t.Fatalf("column %d: %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, p, s, d, n, nok)
		}
	}
}
func (c *fdwoCase) step(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fdwoColumns, fdwoTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cols, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fdwoMetadata(t, cols, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if want[row][column] == nil {
			if v != nil {
				t.Fatalf("NULL=%T(%v)", v, v)
			}
			return true
		}
		text, ok := v.(string)
		if c.b.PG && column < 3 {
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
func (c *fdwoCase) check(e fixture.Executor, schema any, want [][]any) {
	c.t.Helper()
	c.run(e, c.step(fdwoQuery, want, schema))
}
func (c *fdwoCase) routes(q string, want [][]any, args ...any) {
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
func (c *fdwoCase) consume(r *sql.Rows, want [][]any) {
	c.t.Helper()
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	fdwoMetadata(c.t, cols, c.b.PG)
	s := c.step("", want)
	n := 0
	for r.Next() {
		if n >= len(want) {
			c.t.Fatal("extra row")
		}
		v := make([]any, 4)
		dst := make([]any, 4)
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
	fdwoMetadata(c.t, cols, c.b.PG)
	if err := r.Close(); err != nil {
		c.t.Fatal(err)
	}
	fdwoMetadata(c.t, cols, c.b.PG)
}
