package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const fsopProjection = "foreign_server_catalog,foreign_server_name,option_name,option_value"
const fsopQuery = "SELECT " + fsopProjection + " FROM information_schema.foreign_server_options WHERE foreign_server_name=$1 ORDER BY foreign_server_name,option_name"

var fsopColumns = fixture.FSOColumns
var fsopTypes = []string{"NAME", "NAME", "NAME", "VARCHAR"}
var fsopRoles = []string{"fsop_owner", "fsop_inherit", "fsop_set", "fsop_reader", "fsop_denied", "fsop_newowner", "fsop_group"}

type fsopCase struct {
	t          *testing.T
	p          *fixture.Pair
	b          fixture.Backend
	conn       *sql.Conn
	database   string
	statements []*sql.Stmt
}

func fsopScenario(t *testing.T, body func(*fsopCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, fsopRoles)
	fixture.SchemaReferenceCleanup(t, "fsop_scope")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS fsop_scope CASCADE", "DROP SERVER IF EXISTS fsop_server CASCADE", "DROP SERVER IF EXISTS fsop_second CASCADE", "DROP SERVER IF EXISTS fsop_empty CASCADE", "DROP SERVER IF EXISTS fsop_renamed CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fsop_wrapper CASCADE"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			c := &fsopCase{t: t, p: p, b: backend, conn: conn}
			defer func() {
				for _, statement := range c.statements {
					if err := statement.Close(); err != nil {
						t.Error(err)
					}
				}
			}()
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *fsopCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *fsopCase) exec(qs ...string) {
	c.t.Helper()
	for _, q := range qs {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *fsopCase) seed() {
	c.exec("CREATE FOREIGN DATA WRAPPER fsop_wrapper NO HANDLER NO VALIDATOR OPTIONS(wrapper_only 'excluded')", "CREATE SERVER fsop_server FOREIGN DATA WRAPPER fsop_wrapper OPTIONS(host 'example',password 'private=value',empty '')")
}
func (c *fsopCase) row(wrapper, option, value string) []any {
	return []any{c.database, wrapper, option, value}
}
func (c *fsopCase) seedRows() [][]any {
	return [][]any{c.row("fsop_server", "empty", ""), c.row("fsop_server", "host", "example"), c.row("fsop_server", "password", "private=value")}
}
func (c *fsopCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
	c.t.Helper()
	s, err := e.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.statements = append(c.statements, s)
	return fixture.HeldStatement{Executor: e, Statement: s, SQL: q}
}
func fsopMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
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
		if col.Name() != fsopColumns[i] || col.DatabaseTypeName() != fsopTypes[i] || col.ScanType() != scan || length != wl || lengthOK != wok || p != 0 || s != 0 || d || n != !pg || nok != !pg {
			t.Fatalf("column %d: %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, p, s, d, n, nok)
		}
	}
}
func (c *fsopCase) step(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fsopColumns, fsopTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cols, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fsopMetadata(t, cols, c.b.PG)
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
func (c *fsopCase) check(e fixture.Executor, schema any, want [][]any) {
	c.t.Helper()
	c.run(e, c.step(fsopQuery, want, schema))
}
func (c *fsopCase) routes(q string, want [][]any, args ...any) {
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
func (c *fsopCase) consume(r *sql.Rows, want [][]any) {
	c.t.Helper()
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	fsopMetadata(c.t, cols, c.b.PG)
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
	fsopMetadata(c.t, cols, c.b.PG)
	if err := r.Close(); err != nil {
		c.t.Fatal(err)
	}
	fsopMetadata(c.t, cols, c.b.PG)
}
