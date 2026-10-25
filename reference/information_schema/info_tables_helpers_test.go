package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var itColumns = strings.Fields("table_catalog table_schema table_name table_type self_referencing_column_name reference_generation user_defined_type_catalog user_defined_type_schema user_defined_type_name is_insertable_into is_typed commit_action")
var itDomains = strings.Fields("S S S C S C S S S Y Y C")
var itTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "NAME", "VARCHAR", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR"}
var itRoles = []string{"it_owner", "it_reader", "it_member", "it_set", "it_noinherit", "it_other", "it_newowner", "it_super"}

const itSelect = "SELECT * FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2"

type itCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func itScenario(t *testing.T, body func(*itCase)) { t.Helper(); itConfiguredScenario(t, nil, body) }
func itConfiguredScenario(t *testing.T, configure func(*stuntgres.Driver) error, body func(*itCase)) {
	t.Helper()
	for _, schema := range []string{"it_scope", "it_moved", "it_private"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, itRoles)
	p := fixture.NewScenarioWithDriverConfig(t, configure)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS it_scope CASCADE", "DROP SCHEMA IF EXISTS it_moved CASCADE", "DROP SCHEMA IF EXISTS it_private CASCADE", "DROP SERVER IF EXISTS it_live CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS it_read CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS it_insert CASCADE", "DROP EXTENSION IF EXISTS postgres_fdw CASCADE"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Errorf("cleanup %s: %v", q, err)
					}
				}
			})
			c := &itCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			c.exec("CREATE SCHEMA it_scope")
			body(c)
		})
	}
}
func (c *itCase) run(x fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, x, c.b.Recorder, s)
}
func (c *itCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func itRow(database, schema, name, kind, insert string) []any {
	return []any{database, schema, name, kind, nil, nil, nil, nil, nil, insert, "NO", nil}
}
func (c *itCase) row(name, kind, insert string) []any {
	return itRow(c.database, "it_scope", name, kind, insert)
}
func itStep(pg bool, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, itColumns, itTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { itMetadata(t, rows, pg) }
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		raw := make([]any, 12)
		dst := make([]any, 12)
		for i := range raw {
			dst[i] = &raw[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, v := range raw {
			if v == nil {
				continue
			}
			typ := reflect.TypeFor[string]()
			if pg && itDomains[i] == "S" {
				typ = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(v) != typ {
				t.Fatalf("raw %s: %T want %v", itColumns[i], v, typ)
			}
		}
	}
	return s
}
func itMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 12 {
		t.Fatalf("columns=%d want 12", len(cols))
	}
	for i, col := range cols {
		nullable, known := col.Nullable()
		if col.Name() != itColumns[i] || col.DatabaseTypeName() != itTypes[i] || nullable != !pg || known != !pg {
			t.Fatalf("column %d: %s %s nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), nullable, known)
		}
		scan := reflect.TypeFor[string]()
		if pg && itDomains[i] == "S" {
			scan = reflect.TypeFor[any]()
		}
		if col.ScanType() != scan {
			t.Fatalf("%s scan=%v want %v", col.Name(), col.ScanType(), scan)
		}
		length, lengthKnown := col.Length()
		wantLength := int64(0)
		if itDomains[i] == "C" {
			wantLength = math.MaxInt64
			if pg {
				wantLength = -5
			}
		}
		if itDomains[i] == "Y" {
			wantLength = 3
		}
		if length != wantLength || lengthKnown != (itDomains[i] != "S") {
			t.Fatalf("%s length=%d/%t want %d/%t", col.Name(), length, lengthKnown, wantLength, itDomains[i] != "S")
		}
		p, s, k := col.DecimalSize()
		if p != 0 || s != 0 || k {
			t.Fatalf("%s decimal=%d/%d/%t", col.Name(), p, s, k)
		}
	}
}
func (c *itCase) hold(q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := c.conn.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = stmt.Close() })
	return fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: q}
}
func (c *itCase) check(name string, want ...[]any) {
	c.t.Helper()
	s := itStep(c.b.PG, itSelect, want, "it_scope", name)
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
}
func (c *itCase) bag(want ...[]any) {
	c.t.Helper()
	s := itStep(c.b.PG, "SELECT * FROM information_schema.tables WHERE table_schema='it_scope'", want)
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
}
