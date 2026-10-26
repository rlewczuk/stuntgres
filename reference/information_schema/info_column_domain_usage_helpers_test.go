package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var cduColumns = []string{"domain_catalog", "domain_schema", "domain_name", "table_catalog", "table_schema", "table_name", "column_name"}
var cduTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var cduRoles = []string{"cdu_owner", "cdu_table_owner", "cdu_reader", "cdu_member", "cdu_noinherit", "cdu_set"}

func cduScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"cdu", "cdu_moved"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, cduRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS cdu CASCADE", "DROP SCHEMA IF EXISTS cdu_moved CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS cdu_fdw CASCADE"} {
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

func cduMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
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
		if c.Name() != cduColumns[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func cduProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(cduColumns, ",")+" FROM information_schema.column_domain_usage WHERE "+where, cduColumns, cduTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		cduMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
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

func cduRow(database, domainSchema, domain, tableSchema, table, column string) []any {
	return []any{database, domainSchema, domain, database, tableSchema, table, column}
}
func cduSeed(run func(fixture.Step)) {
	for _, q := range []string{"CREATE SCHEMA cdu", "CREATE DOMAIN cdu.parent AS integer", "CREATE DOMAIN cdu.child AS cdu.parent", "CREATE TABLE cdu.t(a cdu.parent,b cdu.child,plain integer)", "CREATE VIEW cdu.v AS SELECT a,b FROM cdu.t"} {
		run(fixture.Exec(q, 0))
	}
}
func cduSeedRows(database string) [][]any {
	return [][]any{cduRow(database, "cdu", "parent", "cdu", "t", "a"), cduRow(database, "cdu", "child", "cdu", "t", "b"), cduRow(database, "cdu", "parent", "cdu", "v", "a"), cduRow(database, "cdu", "child", "cdu", "v", "b")}
}
func cduSelfRows(database string) [][]any {
	var rows [][]any
	for _, column := range cduColumns {
		rows = append(rows, cduRow(database, "information_schema", "sql_identifier", "information_schema", "column_domain_usage", column))
	}
	return rows
}
func cduHold(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}
func cduCheck(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, held, r, s)
}
func cduMissing(run func(fixture.Step)) {
	d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
	d.Position = "8"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.column_domain_usage", d)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
	}
}
func cduIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	var attributes [][]any
	for i, name := range cduColumns {
		attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
	}
	fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.column_domain_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.column_domain_usage'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
}
