package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var rruColumns = []string{"specific_catalog", "specific_schema", "specific_name", "routine_catalog", "routine_schema", "routine_name"}
var rruTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var rruRoles = []string{"rru_caller_owner", "rru_leaf_owner", "rru_reader", "rru_set", "rru_noinherit", "rru_middle", "rru_member", "rru_broken", "rru_other"}

func rruScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, string)) {
	t.Helper()
	for _, schema := range []string{"rru_private", "rru_leaf_schema", "rru_moved"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, rruRoles)
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
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS rru_private CASCADE", "DROP SCHEMA IF EXISTS rru_leaf_schema CASCADE", "DROP SCHEMA IF EXISTS rru_moved CASCADE"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatal(database, err)
			}
			body(t, p, b, c, database)
		})
	}
}

// Resolve declared signatures independently of the usage view and pg_depend.
func rruID(t *testing.T, p *fixture.Pair, c *sql.Conn, schema, name, inputs string) string {
	t.Helper()
	return rsuIdentity(t, p, c, schema, name, inputs)
}
func rruRow(database, sourceSchema, source, targetSchema, target string) []any {
	return []any{database, sourceSchema, source, database, targetSchema, target}
}
func rruMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 6 {
		t.Fatal("projection width", len(columns))
	}
	for i, c := range columns {
		n, nok := c.Nullable()
		l, lok := c.Length()
		precision, scale, dok := c.DecimalSize()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != rruColumns[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || n != !pg || nok != !pg || l != 0 || lok || precision != 0 || scale != 0 || dok {
			t.Fatalf("column %d: %#v nullable=%v/%v scan=%v", i, c, n, nok, c.ScanType())
		}
	}
}
func rruProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.routine_routine_usage WHERE "+where, rruColumns, rruTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		rruMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		_, ok := value.(string)
		if pg {
			_, ok = value.([]byte)
		}
		if !ok {
			t.Fatalf("raw NAME %d/%d = %T(%v)", row, column, value, value)
		}
		return true
	}
	return s
}
func rruCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, where string, want [][]any) {
	t.Helper()
	s := rruProjection(b.PG, where, want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	rsuHeld(t, p, c, b.Recorder, s.Query)(s)
}
func rruExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}
