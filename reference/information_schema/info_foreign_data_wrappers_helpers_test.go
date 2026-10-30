package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const fdwvProjection = "foreign_data_wrapper_catalog,foreign_data_wrapper_name,authorization_identifier,library_name,foreign_data_wrapper_language"
const fdwvQuery = "SELECT " + fdwvProjection + " FROM information_schema.foreign_data_wrappers WHERE foreign_data_wrapper_name=$1 ORDER BY foreign_data_wrapper_name"

var fdwvColumns = []string{"foreign_data_wrapper_catalog", "foreign_data_wrapper_name", "authorization_identifier", "library_name", "foreign_data_wrapper_language"}
var fdwvTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "VARCHAR"}
var fdwvRoles = []string{"fdwview_owner", "fdwview_inherit", "fdwview_set", "fdwview_reader", "fdwview_denied", "fdwview_newowner", "fdwview_group", "fdwview_renamedowner"}

type fdwvCase struct {
	t          *testing.T
	p          *fixture.Pair
	b          fixture.Backend
	conn       *sql.Conn
	database   string
	owner      string
	statements []*sql.Stmt
}

func fdwvScenario(t *testing.T, body func(*testing.T, *fdwvCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, fdwvRoles)
	fixture.SchemaReferenceCleanup(t, "fdwview_scope")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS fdwview_scope CASCADE", "DROP SERVER IF EXISTS fdwview_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwview_wrapper CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwview_second CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwview_empty CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwview_renamed CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdwview_tx CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			c := &fdwvCase{t: t, p: p, b: backend, conn: conn}
			defer func() {
				for _, statement := range c.statements {
					if err := statement.Close(); err != nil {
						t.Error(err)
					}
				}
			}()
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database(),current_user").Scan(&c.database, &c.owner); err != nil {
				t.Fatal(err)
			}
			body(t, c)
		})
	}
}

func (c *fdwvCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *fdwvCase) exec(qs ...string) {
	c.t.Helper()
	for _, q := range qs {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *fdwvCase) seed() {
	c.exec("CREATE FOREIGN DATA WRAPPER fdwview_wrapper NO HANDLER NO VALIDATOR OPTIONS(remote 'é=🙂',password 'private=value',empty '')")
}
func (c *fdwvCase) row(wrapper, owner string) []any {
	return []any{c.database, wrapper, owner, nil, "c"}
}
func (c *fdwvCase) seedRows() [][]any {
	return [][]any{c.row("fdwview_wrapper", c.owner)}
}
func (c *fdwvCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
	c.t.Helper()
	s, err := e.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.statements = append(c.statements, s)
	return fixture.HeldStatement{Executor: e, Statement: s, SQL: q}
}
func fdwvMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(cols) != 5 {
		t.Fatalf("columns=%d want 5", len(cols))
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if pg && i < 3 {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		wl, wok := int64(0), false
		if i >= 3 {
			wl, wok = math.MaxInt64, true
			if pg {
				wl = -5
			}
		}
		p, s, d := col.DecimalSize()
		n, nok := col.Nullable()
		if col.Name() != fdwvColumns[i] || col.DatabaseTypeName() != fdwvTypes[i] || col.ScanType() != scan || length != wl || lengthOK != wok || p != 0 || s != 0 || d || n != !pg || nok != !pg {
			t.Fatalf("column %d: %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, p, s, d, n, nok)
		}
	}
}
func (c *fdwvCase) step(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fdwvColumns, fdwvTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cols, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fdwvMetadata(t, cols, c.b.PG)
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
func (c *fdwvCase) check(e fixture.Executor, schema any, want [][]any) {
	c.t.Helper()
	c.run(e, c.step(fdwvQuery, want, schema))
}
func (c *fdwvCase) routes(q string, want [][]any, args ...any) {
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
func (c *fdwvCase) consume(r *sql.Rows, want [][]any) {
	c.t.Helper()
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	fdwvMetadata(c.t, cols, c.b.PG)
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
	fdwvMetadata(c.t, cols, c.b.PG)
	if err := r.Close(); err != nil {
		c.t.Fatal(err)
	}
	fdwvMetadata(c.t, cols, c.b.PG)
}
