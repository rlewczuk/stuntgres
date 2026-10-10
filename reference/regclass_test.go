package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func regclassRows(query, column string, values ...any) referenceStep {
	rows := make([][]any, len(values))
	for i, value := range values {
		rows[i] = []any{value}
	}
	return referenceSelect(query, []string{column}, []string{"REGCLASS"}, rows)
}

func regclassError(query, code, message, position, portal string, args ...any) referenceStep {
	want := controlsReferenceDiagnostic(code, message)
	want.Position, want.Context = position, portal
	s := referenceErrorDiagnostic(query, want, args...)
	s.selectRows = true
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
	schemaReferenceCleanup(t, "rr_a", "rr_b")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, ctx, c, backend.recorder, s) }
			for _, q := range []string{"CREATE SCHEMA rr_a", "CREATE SCHEMA rr_b", "CREATE TABLE rr_a.rr_target(v INT)", "CREATE TABLE rr_b.rr_target(v INT)", "CREATE VIEW rr_a.rr_view AS SELECT v FROM rr_a.rr_target", "CREATE INDEX rr_index ON rr_a.rr_target(v)", "CREATE SEQUENCE rr_a.rr_sequence", "SET search_path=rr_a,rr_b"} {
				run(referenceExec(q, 0))
			}
			for _, tc := range []struct{ input, want string }{{"rr_target", "rr_target"}, {"rr_a.rr_target", "rr_target"}, {"rr_b.rr_target", "rr_b.rr_target"}, {"rr_view", "rr_view"}, {"rr_index", "rr_index"}, {"rr_sequence", "rr_sequence"}} {
				run(regclassRows("SELECT '"+tc.input+"'::regclass AS r", "r", tc.want))
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
			run(referenceExec("SET search_path=rr_b,rr_a", 0))
			run(referenceSelect("SELECT 'rr_target'::regclass::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{b}}))
			run(regclassRows("SELECT 'rr_a.rr_target'::regclass AS r", "r", "rr_a.rr_target"))
			for _, name := range []string{"Mixed", "select", "a.b", "a\"b", "rr--suffix", "rr/*suffix*/", strings.Repeat("é", 31)} {
				quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
				run(referenceExec("CREATE TABLE rr_a."+quoted+"(v INT)", 0))
				s := regclassRows("SELECT $1::regclass", "regclass", quoted)
				s.args = []any{"rr_a." + quoted}
				run(s)
				run(referencePrepared(s))
			}
			var database string
			if err := c.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			if database == "" {
				t.Fatal("empty database identity")
			}
			s := regclassRows("SELECT $1::regclass", "regclass", "rr_a.rr_target")
			s.args = []any{`"` + strings.ReplaceAll(database, `"`, `""`) + `".rr_a.rr_target`}
			run(s)
			run(referenceExec("SET search_path=rr_absent,rr_a", 0))
			run(regclassRows("SELECT 'rr_target'::regclass AS r", "r", "rr_target"))
		})
	}
}

func TestReferenceRegclassNumericConversions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, ctx, c, backend.recorder, s) }
			for _, q := range []string{"CREATE TABLE rr_numeric(i INT, b BIGINT, o OID, s TEXT)", "CREATE VIEW rr_numeric_view AS SELECT i FROM rr_numeric", "CREATE INDEX rr_numeric_index ON rr_numeric(i)", "CREATE SEQUENCE rr_numeric_sequence"} {
				run(referenceExec(q, 0))
			}
			q := "SELECT $1::regclass AS r"
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			for _, tc := range []struct {
				input         any
				want          any
				code, message string
			}{
				{"rr_numeric", "rr_numeric", "", ""}, {"0", "-", "", ""}, {"-", "-", "", ""}, {"4294967295", "4294967295", "", ""}, {"010", "8", "", ""}, {nil, nil, "", ""},
				{"4294967296", nil, "22003", `value "4294967296" is out of range for type oid`}, {"08", nil, "22P02", `invalid input syntax for type oid: "08"`}, {"+1", nil, "42P01", `relation "+1" does not exist`}, {"-1", nil, "42P01", `relation "-1" does not exist`}, {" 42 ", nil, "42P01", `relation "42" does not exist`},
			} {
				s := regclassRows(q, "r", tc.want)
				s.args = []any{tc.input}
				if tc.code != "" {
					s = regclassError(q, tc.code, tc.message, "", "unnamed portal parameter $1 = '...'", tc.input)
				}
				run(s)
				referenceRunStep(t, ctx, held, backend.recorder, s)
				if tc.input != nil {
					literal := "SELECT '" + tc.input.(string) + "'::regclass AS r"
					s = regclassRows(literal, "r", tc.want)
					if tc.code != "" {
						s = regclassError(literal, tc.code, tc.message, "8", "")
					}
					run(s)
				}
			}
			for _, tc := range []struct{ expression, want string }{{"0::regclass", "-"}, {"(-1)::regclass", "4294967295"}, {"2147483647::regclass", "2147483647"}, {"2147483648::regclass", "2147483648"}, {"4294967295::regclass", "4294967295"}, {"(-2147483648)::regclass", "2147483648"}, {"'rr_numeric'::regclass::text", "rr_numeric"}} {
				s := regclassRows("SELECT "+tc.expression+" AS r", "r", tc.want)
				if strings.HasSuffix(tc.expression, "::text") {
					s.types = []string{"TEXT"}
				}
				run(s)
				run(referencePrepared(s))
			}
			run(regclassError("SELECT 4294967296::regclass AS r", "22003", "OID out of range", "", ""))
			run(regclassError("SELECT true::regclass AS r", "42846", "cannot cast type boolean to regclass", "12", ""))
			run(regclassError("SELECT 1.5::regclass AS r", "42846", "cannot cast type numeric to regclass", "11", ""))
			for _, tc := range []struct {
				expression, typ string
				want            int64
			}{{"'4294967295'::regclass::integer", "INT4", -1}, {"'4294967295'::regclass::bigint", "INT8", 4294967295}} {
				run(referenceSelect("SELECT "+tc.expression+" AS r", []string{"r"}, []string{tc.typ}, [][]any{{tc.want}}))
			}
			for _, name := range []string{"rr_numeric", "rr_numeric_view", "rr_numeric_index", "rr_numeric_sequence"} {
				var oid int64
				if err := c.QueryRowContext(ctx, "SELECT '"+name+"'::regclass::oid").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				run(regclassRows(fmt.Sprintf("SELECT %d::oid::regclass AS r", oid), "r", name))
				for _, typ := range []string{"integer", "bigint"} {
					databaseType := "INT4"
					if typ == "bigint" {
						databaseType = "INT8"
					}
					run(referenceSelect("SELECT '"+name+"'::regclass::"+typ+" AS id", []string{"id"}, []string{databaseType}, [][]any{{oid}}))
				}
			}
			run(referenceExec("INSERT INTO rr_numeric VALUES('rr_numeric'::regclass::integer,'rr_numeric'::regclass::bigint,'rr_numeric'::regclass::oid,'rr_numeric')", 1))
			run(referenceSelect("SELECT i::regclass,b::regclass,o::regclass,s::regclass FROM rr_numeric", []string{"i", "b", "o", "s"}, []string{"REGCLASS", "REGCLASS", "REGCLASS", "REGCLASS"}, [][]any{{"rr_numeric", "rr_numeric", "rr_numeric", "rr_numeric"}}))
		})
	}
}

func TestReferenceRegclassMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, ctx, c, backend.recorder, s) }
			for _, q := range []string{"CREATE TABLE rr_metadata_target(v INT)", "CREATE DOMAIN rr_metadata_domain AS regclass", "CREATE DOMAIN rr_metadata_array_domain AS regclass[]", "CREATE TABLE rr_metadata(id INT,r REGCLASS,a REGCLASS[],d rr_metadata_domain,da rr_metadata_array_domain)", "ALTER TABLE rr_metadata ADD COLUMN extra REGCLASS", "INSERT INTO rr_metadata VALUES(1,'rr_metadata_target','{rr_metadata_target}', 'rr_metadata_target','{rr_metadata_target}',NULL),(2,NULL,NULL,NULL,NULL,NULL)"} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 2
				}
				run(referenceExec(q, count))
			}
			steps := []referenceStep{
				regclassRows("SELECT 'rr_metadata_target'::regclass", "regclass", "rr_metadata_target"), regclassRows("SELECT NULL::regclass AS alias", "alias", nil),
				referenceSelect("SELECT r,a,d,da,extra FROM rr_metadata ORDER BY id", []string{"r", "a", "d", "da", "extra"}, []string{"REGCLASS", "_REGCLASS", "REGCLASS", "_REGCLASS", "REGCLASS"}, [][]any{{"rr_metadata_target", "{rr_metadata_target}", "rr_metadata_target", "{rr_metadata_target}", nil}, {nil, nil, nil, nil, nil}}),
				referenceSelect("SELECT r,a,d,da FROM rr_metadata WHERE id=99", []string{"r", "a", "d", "da"}, []string{"REGCLASS", "_REGCLASS", "REGCLASS", "_REGCLASS"}, nil),
			}
			for i, s := range steps {
				expression := i < 2
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { regclassMetadata(t, rows, backend.pg, expression) }
				run(s)
				run(referencePrepared(s))
				stmt, err := c.PrepareContext(ctx, s.query)
				if err != nil {
					t.Fatal(err)
				}
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: s.query}
				referenceRunStep(t, ctx, held, backend.recorder, s)
				referenceRunStep(t, ctx, held, backend.recorder, s)
				stmt.Close()
			}
			run(referencePrepared(referenceExec("UPDATE rr_metadata SET r=$1 WHERE id=2", 1, "rr_metadata_target")))
			run(regclassRows("SELECT r FROM rr_metadata ORDER BY id", "r", "rr_metadata_target", "rr_metadata_target"))
		})
	}
}
