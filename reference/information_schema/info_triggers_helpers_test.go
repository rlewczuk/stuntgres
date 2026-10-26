package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var itrColumns = []string{"trigger_catalog", "trigger_schema", "trigger_name", "event_manipulation", "event_object_catalog", "event_object_schema", "event_object_table", "action_order", "action_condition", "action_statement", "action_orientation", "action_timing", "action_reference_old_table", "action_reference_new_table", "action_reference_old_row", "action_reference_new_row", "created"}
var itrTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "NAME", "NAME", "NAME", "INT4", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "NAME", "NAME", "NAME", "NAME", "TIMESTAMPTZ"}
var itrRoles = []string{"it_owner", "it_reader", "it_inherit", "it_noinherit"}

func itrScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"it_scope", "it_moved", "it_shadow", "it_function", "it_renamed"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, itrRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS it_scope CASCADE", "DROP SCHEMA IF EXISTS it_moved CASCADE", "DROP SCHEMA IF EXISTS it_shadow CASCADE", "DROP SCHEMA IF EXISTS it_function CASCADE", "DROP SCHEMA IF EXISTS it_renamed CASCADE"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			body(t, p, b, c, r, database)
		})
	}
}

func itSeed(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE SCHEMA it_scope",
		"CREATE TABLE it_scope.it_data(id integer,v integer,other integer)",
		"CREATE TABLE it_scope.it_second(id integer,v integer)",
		"CREATE FUNCTION public.it_identity() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TRIGGER a BEFORE INSERT ON it_scope.it_data FOR EACH ROW EXECUTE FUNCTION public.it_identity()",
		"CREATE TRIGGER m BEFORE UPDATE OF v ON it_scope.it_data FOR EACH ROW EXECUTE FUNCTION public.it_identity()",
		"CREATE TRIGGER z BEFORE INSERT OR UPDATE ON it_scope.it_data FOR EACH ROW WHEN (NEW.v>0) EXECUTE FUNCTION public.it_identity()",
		"CREATE TRIGGER a BEFORE INSERT ON it_scope.it_second FOR EACH ROW EXECUTE FUNCTION public.it_identity()",
	} {
		run(fixture.Exec(q, 0))
	}
}

func itrRow(database, schema, table, name, event string, rank int64, condition any, statement string) []any {
	return []any{database, schema, name, event, database, schema, table, rank, condition, statement, "ROW", "BEFORE", nil, nil, nil, nil, nil}
}
func itSeedRows(database string, disclose bool) [][]any {
	var condition any
	if disclose {
		condition = "(new.v > 0)"
	}
	return [][]any{
		itrRow(database, "it_scope", "it_data", "a", "INSERT", 1, nil, "EXECUTE FUNCTION it_identity()"),
		itrRow(database, "it_scope", "it_data", "m", "UPDATE", 1, nil, "EXECUTE FUNCTION it_identity()"),
		itrRow(database, "it_scope", "it_data", "z", "INSERT", 2, condition, "EXECUTE FUNCTION it_identity()"),
		itrRow(database, "it_scope", "it_data", "z", "UPDATE", 2, condition, "EXECUTE FUNCTION it_identity()"),
		itrRow(database, "it_scope", "it_second", "a", "INSERT", 1, nil, "EXECUTE FUNCTION it_identity()"),
	}
}
func itProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.triggers WHERE "+where, itrColumns, itrTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		itrMetadata(t, columns, pg)
	}
	return s
}
func itrMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 17 {
		t.Fatal("width", len(columns))
	}
	for i, c := range columns {
		nullable, known := c.Nullable()
		scan := reflect.TypeFor[string]()
		if itrTypes[i] == "NAME" && pg {
			scan = reflect.TypeFor[any]()
		}
		if i == 7 {
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		}
		if i == 16 {
			scan = reflect.TypeFor[time.Time]()
		}
		if c.Name() != itrColumns[i] || c.DatabaseTypeName() != itrTypes[i] || c.ScanType() != scan || nullable != !pg || known != !pg {
			t.Fatalf("column %d: %s %s %v nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), nullable, known)
		}
		length, lengthKnown := c.Length()
		expectedLength, expectedLengthKnown := int64(0), false
		if itrTypes[i] == "VARCHAR" {
			expectedLength, expectedLengthKnown = int64(9223372036854775807), true
			if pg {
				expectedLength = -5
			}
		}
		precision, scale, decimalKnown := c.DecimalSize()
		if length != expectedLength || lengthKnown != expectedLengthKnown || precision != 0 || scale != 0 || decimalKnown {
			t.Fatalf("column %d length=%d/%t decimal=%d/%d/%t", i, length, lengthKnown, precision, scale, decimalKnown)
		}
	}
}
func itHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, query string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, r, s) }
}
