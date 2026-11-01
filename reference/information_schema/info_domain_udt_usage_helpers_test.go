package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var duColumns = []string{"udt_catalog", "udt_schema", "udt_name", "domain_catalog", "domain_schema", "domain_name"}
var duTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var duRoles = []string{"du_owner", "du_domain_owner", "du_reader", "du_member", "du_middle", "du_leaf", "du_blocked", "du_set"}

func duScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"du_scope", "du_moved", "Du Quoted", "du_smoke"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, duRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS du_scope CASCADE", "DROP SCHEMA IF EXISTS du_moved CASCADE", "DROP SCHEMA IF EXISTS \"Du Quoted\" CASCADE", "DROP SCHEMA IF EXISTS du_smoke CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("database=%q error=%v", database, err)
			}
			body(t, p, b, c, b.Recorder, database)
		})
	}
}

func duMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
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
		if c.Name() != duColumns[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func duProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(duColumns, ",")+" FROM information_schema.domain_udt_usage WHERE "+where, duColumns, duTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		duMetadata(t, columns, pg)
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

func duRow(database, baseSchema, base, domainSchema, domain string) []any {
	return []any{database, baseSchema, base, database, domainSchema, domain}
}
func duSeed(run func(fixture.Step)) {
	for _, q := range []string{"CREATE SCHEMA du_scope", "CREATE DOMAIN du_scope.p AS integer", "CREATE DOMAIN du_scope.c AS du_scope.p", "CREATE DOMAIN du_scope.arr AS integer[]", "CREATE TYPE du_scope.e AS ENUM('x')", "CREATE DOMAIN du_scope.de AS du_scope.e", "CREATE TYPE du_scope.rec AS(v integer)", "CREATE DOMAIN du_scope.dr AS du_scope.rec"} {
		run(fixture.Exec(q, 0))
	}
}
func duSeedRows(database string) [][]any {
	return [][]any{duRow(database, "pg_catalog", "int4", "du_scope", "p"), duRow(database, "du_scope", "p", "du_scope", "c"), duRow(database, "pg_catalog", "_int4", "du_scope", "arr"), duRow(database, "du_scope", "e", "du_scope", "de"), duRow(database, "du_scope", "rec", "du_scope", "dr")}
}
func duBuiltinRows(database string) [][]any {
	var rows [][]any
	for _, pair := range [][2]string{{"timestamptz", "time_stamp"}, {"name", "sql_identifier"}, {"varchar", "yes_or_no"}, {"int4", "cardinal_number"}, {"varchar", "character_data"}} {
		rows = append(rows, duRow(database, "pg_catalog", pair[0], "information_schema", pair[1]))
	}
	return rows
}

const duBuiltinWhere = "domain_schema='information_schema' AND domain_name IN ('time_stamp','sql_identifier','yes_or_no','cardinal_number','character_data')"

func duCheck(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, held, r, s)
}
func duIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	var attributes [][]any
	for i, name := range duColumns {
		attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
	}
	fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.domain_udt_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select(`SELECT c.relkind,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.domain_udt_usage'::regclass`, []string{"relkind", "same_type", "same_relation", "array_element", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true}}))
}

func duRetained(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	used := make([]bool, len(want))
	count := 0
	for rows.Next() {
		var cells [6]any
		if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5]); err != nil {
			t.Fatal(err)
		}
		for i := range cells {
			cells[i] = fixture.Normalize(cells[i])
		}
		matched := false
		for i, expected := range want {
			if !used[i] && reflect.DeepEqual(cells[:], expected) {
				used[i], matched = true, true
				break
			}
		}
		if !matched {
			t.Fatalf("unexpected retained row %v", cells)
		}
		count++
	}
	if rows.Err() != nil || count != len(want) {
		t.Fatalf("retained count=%d want=%d error=%v", count, len(want), rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
