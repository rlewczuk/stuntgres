package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var cuColumns = []string{"udt_catalog", "udt_schema", "udt_name", "table_catalog", "table_schema", "table_name", "column_name"}
var cuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var cuRoles = []string{"cu_type_owner", "cu_parent_owner", "cu_child_owner", "cu_table_owner", "cu_reader", "cu_member", "cu_middle", "cu_leaf", "cu_blocked", "cu_set"}

func cuScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"cu_scope", "cu_moved", "cu_renamed", "cu_smoke"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, cuRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS cu_scope CASCADE", "DROP SCHEMA IF EXISTS cu_moved CASCADE", "DROP SCHEMA IF EXISTS cu_renamed CASCADE", "DROP SCHEMA IF EXISTS cu_smoke CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS cu_fdw CASCADE"} {
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

func cuMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 7 {
		t.Fatalf("columns=%d want 7", len(columns))
	}
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != cuColumns[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func cuProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(cuColumns, ",")+" FROM information_schema.column_udt_usage WHERE "+where, cuColumns, cuTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		cuMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			for _, candidate := range want {
				if candidate[column] == nil {
					return true
				}
			}
			t.Fatalf("unexpected NULL column %d", column)
		}
		text, ok := value.(string)
		if pg {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok {
			t.Fatalf("raw column %d=%T(%v)", column, value, value)
		}
		for _, candidate := range want {
			if candidate[column] == text {
				return true
			}
		}
		t.Fatalf("unexpected raw column %d=%q", column, text)
		return true
	}
	return s
}

func cuRow(database, typeSchema, typ, tableSchema, table, column string) []any {
	return []any{database, typeSchema, typ, database, tableSchema, table, column}
}

func cuSeed(run func(fixture.Step)) {
	for _, q := range []string{"CREATE SCHEMA cu_scope", "CREATE DOMAIN cu_scope.p AS integer", "CREATE DOMAIN cu_scope.c AS cu_scope.p", "CREATE TABLE cu_scope.t(a cu_scope.p,b cu_scope.c,plain integer)", "CREATE VIEW cu_scope.v AS SELECT a,b FROM cu_scope.t"} {
		run(fixture.Exec(q, 0))
	}
}

func cuSeedRows(database string) [][]any {
	return [][]any{cuRow(database, "pg_catalog", "int4", "cu_scope", "t", "a"), cuRow(database, "cu_scope", "p", "cu_scope", "t", "b"), cuRow(database, "pg_catalog", "int4", "cu_scope", "t", "plain"), cuRow(database, "pg_catalog", "int4", "cu_scope", "v", "a"), cuRow(database, "cu_scope", "p", "cu_scope", "v", "b")}
}

func cuSelfRows(database string) [][]any {
	var rows [][]any
	for _, column := range cuColumns {
		rows = append(rows, cuRow(database, "pg_catalog", "name", "information_schema", "column_udt_usage", column))
	}
	return rows
}

func cuCheck(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, held, r, s)
}

func cuIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	var attributes, columns [][]any
	for i, name := range cuColumns {
		attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		columns = append(columns, []any{name, int64(i + 1), "YES", "name", "pg_catalog", "name", "information_schema", "sql_identifier"})
	}
	fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.column_udt_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select(`SELECT c.relkind,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.column_udt_usage'::regclass`, []string{"relkind", "same_type", "same_relation", "array_element", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select(`SELECT column_name,ordinal_position,is_nullable,data_type,udt_schema,udt_name,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='column_udt_usage' ORDER BY ordinal_position`, []string{"column_name", "ordinal_position", "is_nullable", "data_type", "udt_schema", "udt_name", "domain_schema", "domain_name"}, []string{"NAME", "INT4", "VARCHAR", "VARCHAR", "NAME", "NAME", "NAME", "NAME"}, columns))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select(`SELECT pg_typeof(udt_catalog)::text AS logical_type FROM information_schema.column_udt_usage WHERE table_schema='information_schema' AND table_name='column_udt_usage' AND column_name='udt_catalog'`, []string{"logical_type"}, []string{"TEXT"}, [][]any{{"information_schema.sql_identifier"}}))
}
