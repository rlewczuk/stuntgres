package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var vruColumns = []string{"table_catalog", "table_schema", "table_name", "specific_catalog", "specific_schema", "specific_name"}
var vruTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var vruRoles = []string{"vru_a", "vru_b", "vru_c", "vru_reader", "vru_set", "vru_noinherit", "vru_middle", "vru_leaf", "vru_broken"}

func vruScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"vru_scope", "vru_other", "vru_private", "vru_moved", "vru_renamed"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, vruRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "GRANT SELECT ON information_schema.view_routine_usage TO PUBLIC", "DROP SCHEMA IF EXISTS vru_scope CASCADE", "DROP SCHEMA IF EXISTS vru_other CASCADE", "DROP SCHEMA IF EXISTS vru_private CASCADE", "DROP SCHEMA IF EXISTS vru_moved CASCADE", "DROP SCHEMA IF EXISTS vru_renamed CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("database=%q error=%v", database, err)
			}
			body(t, p, b, c, r, database)
		})
	}
}

func vruMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	vruMetadataNames(t, columns, vruColumns, pg)
}

func vruMetadataNames(t *testing.T, columns []*sql.ColumnType, names []string, pg bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("columns=%d want %d", len(columns), len(names))
	}
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != names[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func vruProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.view_routine_usage WHERE "+where, vruColumns, vruTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		vruMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		text, ok := value.(string)
		if pg {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok {
			t.Fatalf("raw NAME column %d=%T(%v)", column, value, value)
		}
		for _, candidate := range want {
			if candidate[column] == text {
				return true
			}
		}
		t.Fatalf("unexpected raw NAME column %d=%q", column, text)
		return true
	}
	return s
}

func vruRow(database, viewSchema, view, routineSchema, identity string) []any {
	return []any{database, viewSchema, view, database, routineSchema, identity}
}

func vruHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, query string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, r, s) }
}

func vruSeed(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE SCHEMA vru_scope",
		"CREATE FUNCTION vru_scope.h() RETURNS integer RETURN 7",
		"CREATE FUNCTION vru_scope.f() RETURNS integer RETURN vru_scope.h()",
		"CREATE FUNCTION vru_scope.g() RETURNS integer RETURN 9",
		"CREATE FUNCTION vru_scope.null_f() RETURNS integer RETURN NULL::integer",
		"CREATE FUNCTION vru_scope.overloaded(integer) RETURNS integer RETURN $1",
		"CREATE FUNCTION vru_scope.overloaded(bigint) RETURNS integer RETURN 1",
		"CREATE VIEW vru_scope.v AS SELECT vru_scope.f()+vru_scope.f() AS x",
		"CREATE VIEW vru_scope.empty_v AS SELECT abs(-1) AS x",
	} {
		run(fixture.Exec(q, 0))
	}
}

func vruSeedRows(t *testing.T, p *fixture.Pair, c *sql.Conn, database string) [][]any {
	t.Helper()
	return [][]any{vruRow(database, "vru_scope", "v", "vru_scope", rsuIdentity(t, p, c, "vru_scope", "f", ""))}
}

func vruCheckBag(t *testing.T, got, want [][]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("rows=%v want=%v", got, want)
	}
	used := make([]bool, len(got))
	for _, expected := range want {
		found := false
		for i, actual := range got {
			if !used[i] && reflect.DeepEqual(actual, expected) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			t.Fatalf("rows=%v missing=%v", got, expected)
		}
	}
}
