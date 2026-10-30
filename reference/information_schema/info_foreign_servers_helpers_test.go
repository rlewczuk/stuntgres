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

const fsvProjection = "foreign_server_catalog,foreign_server_name,foreign_data_wrapper_catalog,foreign_data_wrapper_name,foreign_server_type,foreign_server_version,authorization_identifier"
const fsvQuery = "SELECT " + fsvProjection + " FROM information_schema.foreign_servers WHERE foreign_server_name=$1 ORDER BY foreign_server_name"

var fsvColumns = []string{"foreign_server_catalog", "foreign_server_name", "foreign_data_wrapper_catalog", "foreign_data_wrapper_name", "foreign_server_type", "foreign_server_version", "authorization_identifier"}
var fsvTypes = []string{"NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "NAME"}
var fsvRoles = []string{"fsv_owner", "fsv_inherit", "fsv_set", "fsv_reader", "fsv_denied", "fsv_newowner", "fsv_group", "fsv_renamedowner"}

type fsvCase struct {
	t          *testing.T
	p          *fixture.Pair
	b          fixture.Backend
	conn       *sql.Conn
	database   string
	owner      string
	statements []*sql.Stmt
}

func fsvScenario(t *testing.T, body func(*testing.T, *fsvCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, fsvRoles)
	fixture.SchemaReferenceCleanup(t, "fsv_scope")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS fsv_scope CASCADE", "DROP SERVER IF EXISTS fsv_null CASCADE", "DROP SERVER IF EXISTS fsv_empty CASCADE", "DROP SERVER IF EXISTS fsv_text CASCADE", "DROP SERVER IF EXISTS fsv_renamed CASCADE", "DROP SERVER IF EXISTS fsv_tx CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fsv_wrapper CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fsv_renamed_wrapper CASCADE"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			c := &fsvCase{t: t, p: p, b: backend, conn: conn}
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

func (c *fsvCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *fsvCase) exec(qs ...string) {
	c.t.Helper()
	for _, q := range qs {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *fsvCase) wrapper() {
	c.exec("CREATE FOREIGN DATA WRAPPER fsv_wrapper NO HANDLER NO VALIDATOR OPTIONS(remote 'é=🙂',password 'wrapper-secret',empty '')")
}
func (c *fsvCase) seed() {
	c.wrapper()
	c.exec("CREATE SERVER fsv_text TYPE 'é=🙂' VERSION 'before' FOREIGN DATA WRAPPER fsv_wrapper OPTIONS(password 'server-secret',empty '')")
}
func (c *fsvCase) row(server, wrapper, owner string, kind, version any) []any {
	return []any{c.database, server, c.database, wrapper, kind, version, owner}
}
func (c *fsvCase) seedRows() [][]any {
	return [][]any{c.row("fsv_text", "fsv_wrapper", c.owner, "é=🙂", "before")}
}
func (c *fsvCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
	c.t.Helper()
	s, err := e.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.statements = append(c.statements, s)
	return fixture.HeldStatement{Executor: e, Statement: s, SQL: q}
}
func fsvMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(cols) != 7 {
		t.Fatalf("columns=%d want 7", len(cols))
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if pg && (i < 4 || i == 6) {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		wl, wok := int64(0), false
		if i == 4 || i == 5 {
			wl, wok = math.MaxInt64, true
			if pg {
				wl = -5
			}
		}
		p, s, d := col.DecimalSize()
		n, nok := col.Nullable()
		if col.Name() != fsvColumns[i] || col.DatabaseTypeName() != fsvTypes[i] || col.ScanType() != scan || length != wl || lengthOK != wok || p != 0 || s != 0 || d || n != !pg || nok != !pg {
			t.Fatalf("column %d: %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, p, s, d, n, nok)
		}
	}
}
func (c *fsvCase) step(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fsvColumns, fsvTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cols, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fsvMetadata(t, cols, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if want[row][column] == nil {
			if v != nil {
				t.Fatalf("NULL=%T(%v)", v, v)
			}
			return true
		}
		text, ok := v.(string)
		if c.b.PG && (column < 4 || column == 6) {
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
func (c *fsvCase) check(e fixture.Executor, schema any, want [][]any) {
	c.t.Helper()
	c.run(e, c.step(fsvQuery, want, schema))
}
func (c *fsvCase) routes(q string, want [][]any, args ...any) {
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
func (c *fsvCase) consume(r *sql.Rows, want [][]any) {
	c.t.Helper()
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	fsvMetadata(c.t, cols, c.b.PG)
	s := c.step("", want)
	n := 0
	for r.Next() {
		if n >= len(want) {
			c.t.Fatal("extra row")
		}
		v := make([]any, 7)
		dst := make([]any, 7)
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
	fsvMetadata(c.t, cols, c.b.PG)
	if err := r.Close(); err != nil {
		c.t.Fatal(err)
	}
	fsvMetadata(c.t, cols, c.b.PG)
}
