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

const umoProjection = "authorization_identifier,foreign_server_catalog,foreign_server_name,option_name,option_value"
const umoQuery = "SELECT " + umoProjection + " FROM information_schema.user_mapping_options WHERE foreign_server_name=$1 ORDER BY authorization_identifier,option_name"

var umoColumns = []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name", "option_name", "option_value"}
var umoTypes = []string{"NAME", "NAME", "NAME", "NAME", "VARCHAR"}
var umoRoles = []string{"umo_owner", "umo_owner_member", "umo_owner_set", "umo_reader", "umo_usage_member", "umo_alice", "umo_alice_member", "umo_bob", "umo_empty", "umo_denied"}

type umoCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	recorder *fixture.Recorder
	notices  *[]fixture.Diagnostic
	database string
}

func umoScenario(t *testing.T, body func(*umoCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, umoRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				defer conn.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP FOREIGN DATA WRAPPER IF EXISTS umo_fdw CASCADE"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			c := &umoCase{t: t, p: p, b: b, conn: conn, recorder: recorder, notices: &notices}
			for _, role := range umoRoles {
				c.exec("CREATE ROLE " + role)
			}
			c.exec("CREATE FOREIGN DATA WRAPPER umo_fdw NO HANDLER NO VALIDATOR OPTIONS(wrapper_only 'decoy')")
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *umoCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.conn, c.recorder, s)
}
func (c *umoCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(fixture.Exec(q, 0))
	}
}
func (c *umoCase) row(role, server, name string, value any) []any {
	return []any{role, c.database, server, name, value}
}
func (c *umoCase) seed() {
	c.exec("CREATE SERVER umo_server FOREIGN DATA WRAPPER umo_fdw OPTIONS(server_only 'decoy')",
		"CREATE USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(empty '',password 'alice=secret',tag 'é=🙂')",
		"CREATE USER MAPPING FOR umo_bob SERVER umo_server OPTIONS(password 'bob-secret')",
		"CREATE USER MAPPING FOR PUBLIC SERVER umo_server OPTIONS(password 'public-secret')",
		"CREATE USER MAPPING FOR umo_empty SERVER umo_server")
}

// Each disclosure mode specifies authority independently of the view's output.
func (c *umoCase) rows(disclose string) [][]any {
	values := []any{"public-secret", "", "alice=secret", "é=🙂", "bob-secret"}
	roles := []string{"PUBLIC", "umo_alice", "umo_alice", "umo_alice", "umo_bob"}
	names := []string{"password", "empty", "password", "tag", "password"}
	var rows [][]any
	for i, role := range roles {
		value := values[i]
		if disclose != "all" && disclose != role {
			value = nil
		}
		rows = append(rows, c.row(role, "umo_server", names[i], value))
	}
	return rows
}

func umoMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 5 {
		t.Fatalf("columns=%d want 5", len(columns))
	}
	for i, col := range columns {
		scan := reflect.TypeFor[string]()
		length, lengthOK := int64(0), false
		if i < 4 && pg {
			scan = reflect.TypeFor[any]()
		}
		if i == 4 {
			length, lengthOK = math.MaxInt64, true
			if pg {
				length = -5
			}
		}
		n, nok := col.Nullable()
		l, lok := col.Length()
		p, s, dok := col.DecimalSize()
		if col.Name() != umoColumns[i] || col.DatabaseTypeName() != umoTypes[i] || col.ScanType() != scan || n != !pg || nok != !pg || l != length || lok != lengthOK || p != 0 || s != 0 || dok {
			t.Fatalf("column %d: name=%q type=%q scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), n, nok, l, lok, p, s, dok)
		}
	}
}

func (c *umoCase) selectStep(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, umoColumns, umoTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		umoMetadata(t, cols, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		expected := want[row][column]
		if expected == nil {
			if value != nil {
				t.Fatalf("raw NULL=%T(%v)", value, value)
			}
			return true
		}
		if column < 4 && c.b.PG {
			v, ok := value.([]byte)
			if !ok || string(v) != expected {
				t.Fatalf("raw NAME=%T(%v) want %v", value, value, expected)
			}
		} else if v, ok := value.(string); !ok || v != expected {
			t.Fatalf("raw text=%T(%v) want %v", value, value, expected)
		}
		return true
	}
	return s
}
func (c *umoCase) routes(query string, want [][]any, args ...any) {
	c.t.Helper()
	s := c.selectStep(query, want, args...)
	c.run(s)
	c.run(fixture.Prepared(s))
}
func (c *umoCase) check(want [][]any) { c.routes(umoQuery, want, "umo_server") }
func (c *umoCase) hold() (*sql.Stmt, func(fixture.Step)) {
	c.t.Helper()
	stmt, err := c.conn.PrepareContext(c.p.Ctx, umoQuery)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: umoQuery}
	return stmt, func(s fixture.Step) { fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, s) }
}
func (c *umoCase) actor(role string, want [][]any) {
	c.t.Helper()
	c.exec("SET SESSION AUTHORIZATION " + role)
	c.check(want)
	c.exec("RESET SESSION AUTHORIZATION")
}
func (c *umoCase) ownership() {
	c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER umo_fdw TO umo_owner", "ALTER SERVER umo_server OWNER TO umo_owner",
		"GRANT USAGE ON FOREIGN SERVER umo_server TO umo_alice,umo_reader")
}
func (c *umoCase) consume(rows *sql.Rows, want [][]any) {
	c.t.Helper()
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	umoMetadata(c.t, cols, c.b.PG)
	var got [][]any
	for rows.Next() {
		cells := make([]any, 5)
		ptrs := make([]any, 5)
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			c.t.Fatal(err)
		}
		for i := range cells {
			cells[i] = fixture.Normalize(cells[i])
		}
		got = append(got, cells)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		c.t.Fatalf("rows=%#v want %#v", got, want)
	}
	umoMetadata(c.t, cols, c.b.PG)
	if err := rows.Close(); err != nil {
		c.t.Fatal(err)
	}
	umoMetadata(c.t, cols, c.b.PG)
}
