package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var vtuColumns = []string{"view_catalog", "view_schema", "view_name", "table_catalog", "table_schema", "table_name"}
var vtuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var vtuRoles = []string{"vtu_a", "vtu_b", "vtu_c", "vtu_reader", "vtu_set", "vtu_noinherit", "vtu_middle", "vtu_leaf", "vtu_broken"}

func vtuScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"vtu_scope", "vtu_other", "vtu_private", "vtu_moved", "vtu_renamed"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, vtuRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS vtu_scope CASCADE", "DROP SCHEMA IF EXISTS vtu_other CASCADE", "DROP SCHEMA IF EXISTS vtu_private CASCADE", "DROP SCHEMA IF EXISTS vtu_moved CASCADE", "DROP SCHEMA IF EXISTS vtu_renamed CASCADE"} {
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

func vtuMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 6 {
		t.Fatalf("columns=%d want 6", len(columns))
	}
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != vtuColumns[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func vtuProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.view_table_usage WHERE "+where, vtuColumns, vtuTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		vtuMetadata(t, columns, pg)
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

func vtuRow(database, viewSchema, view, tableSchema, table string) []any {
	return []any{database, viewSchema, view, database, tableSchema, table}
}

func vtuHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, query string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, r, s) }
}

func vtuSeed(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE SCHEMA vtu_scope", "CREATE TABLE vtu_scope.t(a integer,b integer)", "CREATE TABLE vtu_scope.u(a integer)",
		"CREATE VIEW vtu_scope.v AS SELECT x.a,x.b,y.a AS again FROM vtu_scope.t x JOIN vtu_scope.t y ON x.a=y.a WHERE EXISTS(SELECT 1 FROM vtu_scope.u WHERE u.a=x.a)",
		"CREATE VIEW vtu_scope.w AS SELECT a FROM vtu_scope.v",
	} {
		run(fixture.Exec(q, 0))
	}
}

func vtuSeedRows(database string) [][]any {
	return [][]any{vtuRow(database, "vtu_scope", "v", "vtu_scope", "t"), vtuRow(database, "vtu_scope", "v", "vtu_scope", "u"), vtuRow(database, "vtu_scope", "w", "vtu_scope", "v")}
}
