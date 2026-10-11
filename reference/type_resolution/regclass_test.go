package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func regclassError(query, code, message, position, portal string, args ...any) fixture.Step {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Position, want.Context = position, portal
	s := fixture.ErrorDiagnostic(query, want, args...)
	s.SelectRows = true
	return s
}

func regclassMetadata(t *testing.T, rows *sql.Rows, pg, expression bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		length, lengthKnown := typ.Length()
		precision, scale, decimalKnown := typ.DecimalSize()
		nullable, nullKnown := typ.Nullable()
		if typ.ScanType() != scan || length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown {
			t.Fatalf("metadata %s: scan=%v length=%d/%v decimal=%d,%d/%v", typ.Name(), typ.ScanType(), length, lengthKnown, precision, scale, decimalKnown)
		}
		if pg && (nullable || nullKnown) {
			t.Fatalf("pq nullability %v/%v", nullable, nullKnown)
		}
		if !pg && !expression && (!nullable || !nullKnown) {
			t.Fatalf("native stored nullability %v/%v", nullable, nullKnown)
		}
	}
}

func TestReferenceRegclassResolution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rr_a", "rr_b")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			c, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			for _, q := range []string{"CREATE SCHEMA rr_a", "CREATE SCHEMA rr_b", "CREATE TABLE rr_a.rr_target(v INT)", "CREATE TABLE rr_b.rr_target(v INT)", "CREATE VIEW rr_a.rr_view AS SELECT v FROM rr_a.rr_target", "CREATE INDEX rr_index ON rr_a.rr_target(v)", "CREATE SEQUENCE rr_a.rr_sequence", "SET search_path=rr_a,rr_b"} {
				run(fixture.Exec(q, 0))
			}
			for _, tc := range []struct{ input, want string }{{"rr_target", "rr_target"}, {"rr_a.rr_target", "rr_target"}, {"rr_b.rr_target", "rr_b.rr_target"}, {"rr_view", "rr_view"}, {"rr_index", "rr_index"}, {"rr_sequence", "rr_sequence"}} {
				run(fixture.RegclassRows("SELECT '"+tc.input+"'::regclass AS r", "r", tc.want))
			}
			var a, b int64
			if err := c.QueryRowContext(ctx, "SELECT 'rr_a.rr_target'::regclass::oid").Scan(&a); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(ctx, "SELECT 'rr_b.rr_target'::regclass::oid").Scan(&b); err != nil {
				t.Fatal(err)
			}
			if a <= 0 || b <= 0 || a == b {
				t.Fatalf("invalid distinct identities %d %d", a, b)
			}
			run(fixture.Exec("SET search_path=rr_b,rr_a", 0))
			run(fixture.Select("SELECT 'rr_target'::regclass::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{b}}))
			run(fixture.RegclassRows("SELECT 'rr_a.rr_target'::regclass AS r", "r", "rr_a.rr_target"))
			for _, name := range []string{"Mixed", "select", "a.b", "a\"b", "rr--suffix", "rr/*suffix*/", strings.Repeat("é", 31)} {
				quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
				run(fixture.Exec("CREATE TABLE rr_a."+quoted+"(v INT)", 0))
				s := fixture.RegclassRows("SELECT $1::regclass", "regclass", quoted)
				s.Args = []any{"rr_a." + quoted}
				run(s)
				run(fixture.Prepared(s))
			}
			var database string
			if err := c.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			if database == "" {
				t.Fatal("empty database identity")
			}
			s := fixture.RegclassRows("SELECT $1::regclass", "regclass", "rr_a.rr_target")
			s.Args = []any{`"` + strings.ReplaceAll(database, `"`, `""`) + `".rr_a.rr_target`}
			run(s)
			run(fixture.Exec("SET search_path=rr_absent,rr_a", 0))
			run(fixture.RegclassRows("SELECT 'rr_target'::regclass AS r", "r", "rr_target"))
		})
	}
}

func TestReferenceRegclassNumericConversions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			c, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			for _, q := range []string{"CREATE TABLE rr_numeric(i INT, b BIGINT, o OID, s TEXT)", "CREATE VIEW rr_numeric_view AS SELECT i FROM rr_numeric", "CREATE INDEX rr_numeric_index ON rr_numeric(i)", "CREATE SEQUENCE rr_numeric_sequence"} {
				run(fixture.Exec(q, 0))
			}
			q := "SELECT $1::regclass AS r"
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				input         any
				want          any
				code, message string
			}{
				{"rr_numeric", "rr_numeric", "", ""}, {"0", "-", "", ""}, {"-", "-", "", ""}, {"4294967295", "4294967295", "", ""}, {"010", "8", "", ""}, {nil, nil, "", ""},
				{"4294967296", nil, "22003", `value "4294967296" is out of range for type oid`}, {"08", nil, "22P02", `invalid input syntax for type oid: "08"`}, {"+1", nil, "42P01", `relation "+1" does not exist`}, {"-1", nil, "42P01", `relation "-1" does not exist`}, {" 42 ", nil, "42P01", `relation "42" does not exist`},
			} {
				s := fixture.RegclassRows(q, "r", tc.want)
				s.Args = []any{tc.input}
				if tc.code != "" {
					s = regclassError(q, tc.code, tc.message, "", "unnamed portal parameter $1 = '...'", tc.input)
				}
				run(s)
				fixture.RunStep(t, ctx, held, backend.Recorder, s)
				if tc.input != nil {
					literal := "SELECT '" + tc.input.(string) + "'::regclass AS r"
					s = fixture.RegclassRows(literal, "r", tc.want)
					if tc.code != "" {
						s = regclassError(literal, tc.code, tc.message, "8", "")
					}
					run(s)
				}
			}
			for _, tc := range []struct{ expression, want string }{{"0::regclass", "-"}, {"(-1)::regclass", "4294967295"}, {"2147483647::regclass", "2147483647"}, {"2147483648::regclass", "2147483648"}, {"4294967295::regclass", "4294967295"}, {"(-2147483648)::regclass", "2147483648"}, {"'rr_numeric'::regclass::text", "rr_numeric"}} {
				s := fixture.RegclassRows("SELECT "+tc.expression+" AS r", "r", tc.want)
				if strings.HasSuffix(tc.expression, "::text") {
					s.Types = []string{"TEXT"}
				}
				run(s)
				run(fixture.Prepared(s))
			}
			run(regclassError("SELECT 4294967296::regclass AS r", "22003", "OID out of range", "", ""))
			run(regclassError("SELECT true::regclass AS r", "42846", "cannot cast type boolean to regclass", "12", ""))
			run(regclassError("SELECT 1.5::regclass AS r", "42846", "cannot cast type numeric to regclass", "11", ""))
			for _, tc := range []struct {
				expression, typ string
				want            int64
			}{{"'4294967295'::regclass::integer", "INT4", -1}, {"'4294967295'::regclass::bigint", "INT8", 4294967295}} {
				run(fixture.Select("SELECT "+tc.expression+" AS r", []string{"r"}, []string{tc.typ}, [][]any{{tc.want}}))
			}
			for _, name := range []string{"rr_numeric", "rr_numeric_view", "rr_numeric_index", "rr_numeric_sequence"} {
				var oid int64
				if err := c.QueryRowContext(ctx, "SELECT '"+name+"'::regclass::oid").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				run(fixture.RegclassRows(fmt.Sprintf("SELECT %d::oid::regclass AS r", oid), "r", name))
				for _, typ := range []string{"integer", "bigint"} {
					databaseType := "INT4"
					if typ == "bigint" {
						databaseType = "INT8"
					}
					run(fixture.Select("SELECT '"+name+"'::regclass::"+typ+" AS id", []string{"id"}, []string{databaseType}, [][]any{{oid}}))
				}
			}
			run(fixture.Exec("INSERT INTO rr_numeric VALUES('rr_numeric'::regclass::integer,'rr_numeric'::regclass::bigint,'rr_numeric'::regclass::oid,'rr_numeric')", 1))
			run(fixture.Select("SELECT i::regclass,b::regclass,o::regclass,s::regclass FROM rr_numeric", []string{"i", "b", "o", "s"}, []string{"REGCLASS", "REGCLASS", "REGCLASS", "REGCLASS"}, [][]any{{"rr_numeric", "rr_numeric", "rr_numeric", "rr_numeric"}}))
		})
	}
}

func TestReferenceRegclassMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			c, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			for _, q := range []string{"CREATE TABLE rr_metadata_target(v INT)", "CREATE DOMAIN rr_metadata_domain AS regclass", "CREATE DOMAIN rr_metadata_array_domain AS regclass[]", "CREATE TABLE rr_metadata(id INT,r REGCLASS,a REGCLASS[],d rr_metadata_domain,da rr_metadata_array_domain)", "ALTER TABLE rr_metadata ADD COLUMN extra REGCLASS", "INSERT INTO rr_metadata VALUES(1,'rr_metadata_target','{rr_metadata_target}', 'rr_metadata_target','{rr_metadata_target}',NULL),(2,NULL,NULL,NULL,NULL,NULL)"} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 2
				}
				run(fixture.Exec(q, count))
			}
			steps := []fixture.Step{
				fixture.RegclassRows("SELECT 'rr_metadata_target'::regclass", "regclass", "rr_metadata_target"), fixture.RegclassRows("SELECT NULL::regclass AS alias", "alias", nil),
				fixture.Select("SELECT r,a,d,da,extra FROM rr_metadata ORDER BY id", []string{"r", "a", "d", "da", "extra"}, []string{"REGCLASS", "_REGCLASS", "REGCLASS", "_REGCLASS", "REGCLASS"}, [][]any{{"rr_metadata_target", "{rr_metadata_target}", "rr_metadata_target", "{rr_metadata_target}", nil}, {nil, nil, nil, nil, nil}}),
				fixture.Select("SELECT r,a,d,da FROM rr_metadata WHERE id=99", []string{"r", "a", "d", "da"}, []string{"REGCLASS", "_REGCLASS", "REGCLASS", "_REGCLASS"}, nil),
			}
			for i, s := range steps {
				expression := i < 2
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { regclassMetadata(t, rows, backend.PG, expression) }
				run(s)
				run(fixture.Prepared(s))
				stmt, err := c.PrepareContext(ctx, s.Query)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}
				fixture.RunStep(t, ctx, held, backend.Recorder, s)
				fixture.RunStep(t, ctx, held, backend.Recorder, s)
				stmt.Close()
			}
			run(fixture.Prepared(fixture.Exec("UPDATE rr_metadata SET r=$1 WHERE id=2", 1, "rr_metadata_target")))
			run(fixture.RegclassRows("SELECT r FROM rr_metadata ORDER BY id", "r", "rr_metadata_target", "rr_metadata_target"))
		})
	}
}
