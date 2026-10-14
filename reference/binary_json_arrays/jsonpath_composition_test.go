package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	nativeVisited := false
	want := [][]any{{int64(1), `strict $."a"."v"`, `$."a"."v"`, `{$,NULL,"null"}`}}
	for _, b := range p.Backends() {
		nativeVisited = nativeVisited || !b.PG
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, jsonpathReferenceStep(t, b, s)) }
		run(fixture.Exec("CREATE DOMAIN jp_domain AS jsonpath", 0))
		run(fixture.Exec("CREATE TABLE jp_saved(id INT,p jsonpath DEFAULT jsonpath 'strict $.a.v',d jp_domain,a jsonpath[])", 0))
		inquiry := func(s fixture.Step) { run(jsonpathReferenceInquiry(b, s)) }
		inquiry(fixture.Select("SELECT attname::text,atttypid::bigint,atttypmod,attcollation::bigint FROM pg_catalog.pg_attribute WHERE attrelid='jp_saved'::regclass AND attname='a'", []string{"attname", "atttypid", "atttypmod", "attcollation"}, []string{"TEXT", "INT8", "INT4", "INT8"}, [][]any{{"a", int64(4073), int64(-1), int64(0)}}))
		inquiry(fixture.Select("SELECT attname::text,atttypid::bigint,atttypmod,attcollation::bigint FROM pg_catalog.pg_attribute WHERE attrelid='jp_saved'::regclass AND attname='p'", []string{"attname", "atttypid", "atttypmod", "attcollation"}, []string{"TEXT", "INT8", "INT4", "INT8"}, [][]any{{"p", int64(4072), int64(-1), int64(0)}}))
		var relationID int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'jp_saved'::regclass::oid").Scan(&relationID); err != nil || relationID <= 0 {
			t.Fatal("own relation identity", relationID, err)
		}
		attributes := fixture.Select("SELECT attname,atttypid::bigint,atttypmod,attcollation::bigint FROM pg_catalog.pg_attribute WHERE attrelid=$1 AND attname IN ('p','a') ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation"}, []string{"NAME", "INT8", "INT4", "INT8"}, [][]any{{"p", int64(4072), int64(-1), int64(0)}, {"a", int64(4073), int64(-1), int64(0)}}, relationID)
		run(attributes)
		run(fixture.Prepared(attributes))
		inquiry(fixture.Select("SELECT typbasetype::bigint FROM pg_catalog.pg_type WHERE typname='jp_domain'", []string{"typbasetype"}, []string{"INT8"}, [][]any{{int64(4072)}}))
		run(fixture.Exec("INSERT INTO jp_saved(id,d,a) VALUES(1,$1,$2)", 1, `$.a.v`, `{"$",NULL,"null"}`))
		run(fixture.Exec("CREATE VIEW jp_view AS SELECT id,p,d,a FROM jp_saved", 0))
		for _, table := range []string{"jp_saved", "jp_view"} {
			run(fixture.Select("SELECT id,p,d,a FROM "+table+" ORDER BY id", []string{"id", "p", "d", "a"}, []string{"INT4", "JSONPATH", "JSONPATH", "_JSONPATH"}, want))
		}
		for _, prepared := range []bool{false, true} {
			for _, expr := range []string{
				`'$.a.v'::jsonpath::jsonpath`, `CAST('$.a.v'::text AS jsonpath)`,
				`CAST('$.a.v'::varchar AS jsonpath)`, `CAST('$.a.v'::char(5) AS jsonpath)`,
			} {
				s := fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{`$."a"."v"`}})
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
			for _, tc := range []struct{ target, typ, output string }{
				{"text", "TEXT", `$."a"."v"`},
				{"varchar", "VARCHAR", `$."a"."v"`},
				{"char(20)", "BPCHAR", `$."a"."v"           `},
			} {
				s := fixture.Select("SELECT CAST('$.a.v'::jsonpath AS "+tc.target+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.output}})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					scan := reflect.TypeFor[string]()
					if b.PG && tc.typ == "BPCHAR" {
						scan = reflect.TypeFor[any]()
					}
					if types[0].ScanType() != scan {
						t.Fatalf("%s ScanType=%v want=%v", tc.typ, types[0].ScanType(), scan)
					}
				}
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
			// Explicit character casts size the canonical output by characters,
			// not UTF8 bytes; SQL NULL never passes through text input.
			for _, tc := range []struct {
				expr, typ string
				value     any
			}{
				{`CAST('$.a.v'::jsonpath AS varchar(2))`, "VARCHAR", "$."},
				{`CAST('$.a.v'::jsonpath AS char(2))`, "BPCHAR", "$."},
				{`CAST('"雪é"'::jsonpath AS varchar(3))`, "VARCHAR", `"雪é`},
				{`CAST('"雪é"'::jsonpath AS char(6))`, "BPCHAR", `"雪é"  `},
				{`CAST(NULL::jsonpath AS varchar(2))`, "VARCHAR", nil},
				{`CAST(NULL::jsonpath AS char(2))`, "BPCHAR", nil},
			} {
				s := fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
		}
	}
	if nativeVisited {
		jsonpathReferenceControls(t, p, fixture.Backend{"memory", p.Memory, p.MemoryRecord, false}, want)
	}
}

func TestReferenceJSONPathCompositionProbes(t *testing.T) {
	cases := []struct {
		name, query  string
		diagnostic   fixture.Diagnostic
		prepareError bool
	}{
		{"integer_cast", "SELECT CAST(1 AS jsonpath)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42846", Message: "cannot cast type integer to jsonpath", Position: "8"}, true},
		{"boolean_cast", "SELECT CAST(true AS jsonpath)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42846", Message: "cannot cast type boolean to jsonpath", Position: "8"}, true},
		{"json_cast", `SELECT CAST('{}'::json AS jsonpath)`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42846", Message: "cannot cast type json to jsonpath", Position: "8"}, true},
		{"jsonb_cast", `SELECT CAST('{}'::jsonb AS jsonpath)`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42846", Message: "cannot cast type jsonb to jsonpath", Position: "8"}, true},
		{"to_json", `SELECT CAST('$'::jsonpath AS json)`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42846", Message: "cannot cast type jsonpath to json", Position: "8"}, true},
		{"typmod", `SELECT '$'::jsonpath(2)`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "type modifier is not allowed for type \"jsonpath\"", Position: "13"}, true},
		{"collation", `SELECT '$'::jsonpath COLLATE "C"`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "collations are not supported by type jsonpath", Position: "22"}, true},
		{"equality", `SELECT '$'::jsonpath = '$'::jsonpath`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "operator does not exist: jsonpath = jsonpath", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "22"}, true},
		{"order", `SELECT p FROM jp_boundary ORDER BY p`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type jsonpath", Hint: "Use an explicit ordering operator or modify the query.", Position: "36"}, true},
		{"group", `SELECT p FROM jp_boundary GROUP BY p`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type jsonpath", Position: "36"}, true},
		{"distinct", `SELECT DISTINCT p FROM jp_boundary`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type jsonpath", Position: "17"}, true},
		{"btree", `CREATE INDEX jp_path_idx ON jp_boundary(p)`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "data type jsonpath has no default operator class for access method \"btree\"", Hint: "You must specify an operator class for the index or define a default operator class for the data type."}, false},
		{"array_group", `SELECT a FROM jp_boundary GROUP BY a`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type jsonpath[]", Position: "36"}, true},
		{"domain_group", `SELECT d FROM jp_boundary GROUP BY d`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type jp_bound_domain", Position: "36"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN jp_bound_domain AS jsonpath", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE jp_boundary(p jsonpath,a jsonpath[],d jp_bound_domain)", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, tc.diagnostic)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						if tc.prepareError {
							s.ErrorStage = "prepare"
						}
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		})
	}
	// Assignment casts must not be inferred from explicit CoerceViaIO success.
	for _, tc := range []struct{ source, diagnosticType string }{{"text", "text"}, {"varchar", "character varying"}, {"char(5)", "character"}} {
		source := tc.source
		t.Run("assignment_"+source, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE jp_assignment(p jsonpath)", 0))
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "p" is of type jsonpath but expression is of type ` + tc.diagnosticType, Hint: "You will need to rewrite or cast the expression.", Position: "34"}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO jp_assignment VALUES('$.a.v'::"+source+")", want))
			}
		})
	}
}

// Only the native phase invokes controls. The shared SQL witnesses above and
// actual emitted-SQL replay below have independent expected rows on PostgreSQL.
func jsonpathReferenceControls(t *testing.T, p *fixture.Pair, b fixture.Backend, want [][]any) {
	t.Helper()
	assertRows := func(backend fixture.Backend) {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, jsonpathReferenceStep(t, backend, fixture.Select("SELECT id,p,d,a FROM jp_view ORDER BY id", []string{"id", "p", "d", "a"}, []string{"INT4", "JSONPATH", "JSONPATH", "_JSONPATH"}, want)))
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT p FROM jp_saved ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		assertRows(b)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			return err
		}
		if dump.String() != again.String() {
			t.Fatal("JSONPath dump changed after replay")
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW jp_view; DROP TABLE jp_saved; DROP DOMAIN jp_domain"); err != nil {
				return err
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			assertRows(fixture.Backend{"postgres", p.Postgres, p.PostgresRecord, true})
		}
		prefix := "CREATE TABLE r(p jsonpath); INSERT INTO r VALUES('$'),("
		failed := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"'last')"))
		wantError := jsonpathReferenceDiagnostic(jsonpathReferenceFailures[1])
		wantError.Position = fixture.Position(len(prefix) + 1)
		fixture.AssertDiagnostic(t, failed, &wantError)
		assertRows(b)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			return err
		}
		if dump.String() != after.String() {
			t.Fatal("failed JSONPath restore changed catalog")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE jp_saved(p TEXT); INSERT INTO jp_saved VALUES('new')")); err != nil {
			return err
		}
		if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "JSONPATH" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
			t.Fatal("retained JSONPath metadata changed")
		}
		dst := make([]driver.Value, 1)
		if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{`strict $."a"."v"`}) {
			t.Fatalf("retained JSONPath row=%#v err=%v", dst, err)
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatalf("retained JSONPath EOF: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT p FROM jp_saved", []string{"p"}, []string{"TEXT"}, [][]any{{"new"}}))
}
