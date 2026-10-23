package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var ivColumns = []string{"table_catalog", "table_schema", "table_name", "view_definition", "check_option", "is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"}
var ivTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}
var ivRoles = []string{"iv_owner", "iv_inherit", "iv_set", "iv_noinherit", "iv_reader", "iv_column", "iv_schema", "iv_maintain", "iv_unrelated", "iv_newowner"}

func ivScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, string)) {
	t.Helper()
	for _, schema := range []string{"iv_private", "iv_moved"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, ivRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer c.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS iv_private CASCADE", "DROP SCHEMA IF EXISTS iv_moved CASCADE"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			body(t, p, b, c, database)
		})
	}
}

func ivRow(database, schema, name string, definition any, option, update, insert string) []any {
	return []any{database, schema, name, definition, option, update, insert, "NO", "NO", "NO"}
}

func ivStep(pg bool, query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, ivColumns, ivTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != len(ivColumns) {
			t.Fatalf("columns=%d want 10", len(columns))
		}
		for i, column := range columns {
			nullable, known := column.Nullable()
			if column.Name() != ivColumns[i] || column.DatabaseTypeName() != ivTypes[i] || nullable != !pg || known != !pg {
				t.Fatalf("column %d: name=%q type=%q nullable=%t/%t", i, column.Name(), column.DatabaseTypeName(), nullable, known)
			}
			// pq has no NAME ScanType; VARCHAR has the ordinary string adapter.
			scan := reflect.TypeFor[string]()
			if pg && i < 3 {
				scan = reflect.TypeFor[any]()
			}
			if column.ScanType() != scan {
				t.Fatalf("column %d ScanType=%v want %v", i, column.ScanType(), scan)
			}
			if pg {
				length, lengthKnown := column.Length()
				wantLength := int64(0)
				if i == 3 || i == 4 {
					wantLength = -5
				}
				if i >= 5 {
					wantLength = 3
				}
				if length != wantLength || lengthKnown != (i >= 3) {
					t.Fatalf("column %d Length=%d/%t want %d/%t", i, length, lengthKnown, wantLength, i >= 3)
				}
				precision, scale, decimalKnown := column.DecimalSize()
				if precision != 0 || scale != 0 || decimalKnown {
					t.Fatalf("column %d DecimalSize=%d/%d/%t want 0/0/false", i, precision, scale, decimalKnown)
				}
			}
		}
	}
	return s
}

func ivHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
}

const ivSelect = "SELECT * FROM information_schema.views WHERE table_schema=$1 AND table_name=$2"

func ivFlags(query string, want [][]any, args ...any) fixture.Step {
	return fixture.Select(query,
		[]string{"table_name", "check_option", "is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"},
		[]string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, want, args...)
}

const ivFlagSelect = "SELECT table_name,check_option,is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='public' AND table_name=$1"

func ivFlagRow(name, update, insert string) [][]any {
	return [][]any{{name, "NONE", update, insert, "NO", "NO", "NO"}}
}
