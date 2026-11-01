package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var vcuColumns = []string{"view_catalog", "view_schema", "view_name", "table_catalog", "table_schema", "table_name", "column_name"}
var vcuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var vcuRoles = []string{"vcu_a", "vcu_b", "vcu_c", "vcu_reader", "vcu_set", "vcu_noinherit", "vcu_middle", "vcu_leaf", "vcu_broken"}

func vcuScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"vcu_scope", "vcu_other", "vcu_private", "vcu_moved", "vcu_renamed"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, vcuRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS vcu_scope CASCADE", "DROP SCHEMA IF EXISTS vcu_other CASCADE", "DROP SCHEMA IF EXISTS vcu_private CASCADE", "DROP SCHEMA IF EXISTS vcu_moved CASCADE", "DROP SCHEMA IF EXISTS vcu_renamed CASCADE"} {
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

func vcuMetadataNames(t *testing.T, columns []*sql.ColumnType, names []string, pg bool) {
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

func vcuMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	vcuMetadataNames(t, columns, vcuColumns, pg)
}

func vcuProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.view_column_usage WHERE "+where, vcuColumns, vcuTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		vcuMetadata(t, columns, pg)
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

func vcuRow(database, viewSchema, view, tableSchema, table, column string) []any {
	return []any{database, viewSchema, view, database, tableSchema, table, column}
}

func vcuHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, query string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, r, s) }
}

func vcuSeed(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE SCHEMA vcu_scope", "CREATE TABLE vcu_scope.t(a integer,b integer)", "CREATE TABLE vcu_scope.u(a integer)",
		"CREATE VIEW vcu_scope.v AS SELECT x.a,x.b,y.a AS again FROM vcu_scope.t x JOIN vcu_scope.t y ON x.a=y.a WHERE EXISTS(SELECT 1 FROM vcu_scope.u WHERE u.a=x.a)",
		"CREATE VIEW vcu_scope.w AS SELECT a FROM vcu_scope.v",
	} {
		run(fixture.Exec(q, 0))
	}
}

// REL_18_6 information_schema.sql selects DISTINCT direct attribute dependencies.
// The view w depends on v.a, not transitively on t.a; repeated t.a collapses,
// while t.b remains a separate row. Catalog names come from current_database().
func vcuSeedRows(database string) [][]any {
	return [][]any{
		vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "a"),
		vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "b"),
		vcuRow(database, "vcu_scope", "v", "vcu_scope", "u", "a"),
		vcuRow(database, "vcu_scope", "w", "vcu_scope", "v", "a"),
	}
}
