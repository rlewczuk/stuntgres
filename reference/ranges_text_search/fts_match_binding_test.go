package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type ftsMatchSignature struct {
	name, left, right string
}

var ftsMatchSignatures = []ftsMatchSignature{
	{"ts_match_vq", "tsvector", "tsquery"}, {"ts_match_qv", "tsquery", "tsvector"},
	{"ts_match_tt", "text", "text"}, {"ts_match_tq", "text", "tsquery"},
}

func (s ftsMatchSignature) expression(routine bool) string {
	left, right := "$1::"+s.left, "$2::"+s.right
	if routine {
		return "pg_catalog." + s.name + "(" + left + "," + right + ")"
	}
	return left + " @@ " + right
}

func ftsMatchMetadata(t *testing.T, rows *sql.Rows, label string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 {
		t.Fatal("column count", len(columns))
	}
	c := columns[0]
	if c.Name() != label || c.DatabaseTypeName() != "BOOL" || c.ScanType() != reflect.TypeFor[bool]() {
		t.Fatalf("metadata name=%q type=%q scan=%v", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	if n, known := c.Nullable(); n || known {
		t.Fatal("Nullable", n, known)
	}
	if n, known := c.Length(); n != 0 || known {
		t.Fatal("Length", n, known)
	}
	if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known {
		t.Fatal("DecimalSize", p, s, known)
	}
}

func ftsMatchStep(query, label string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{label}, []string{"BOOL"}, rows, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsMatchMetadata(t, r, label) }
	s.CheckScan = func(t *testing.T, r *sql.Rows, _ int) {
		var raw any
		if err := r.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw != nil {
			if _, ok := raw.(bool); !ok {
				t.Fatalf("raw BOOL = %T(%v)", raw, raw)
			}
		}
	}
	return s
}

func TestReferenceFTSMatchMetadata(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		for _, signature := range ftsMatchSignatures {
			for _, routine := range []bool{false, true} {
				for _, alias := range []string{"", " AS matched"} {
					label := "?column?"
					if routine {
						label = signature.name
					}
					if alias != "" {
						label = "matched"
					}
					query := "SELECT " + signature.expression(routine) + alias
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
						for _, tc := range []struct{ left, right, want any }{{"cat", "cat", true}, {"cat", "dog", false}, {nil, "cat", nil}, {"cat", nil, nil}, {"cat", "cat", true}} {
							s := ftsMatchStep(query, label, [][]any{{tc.want}}, tc.left, tc.right)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
					}()
					zero := ftsMatchStep(query+" WHERE false", label, nil, "cat", "cat")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, zero)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(zero))
					zeroStatement, err := c.PrepareContext(p.Ctx, zero.Query)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer zeroStatement.Close()
						held := fixture.HeldStatement{Executor: c, Statement: zeroStatement, SQL: zero.Query}
						for _, left := range []any{"cat", nil, "dog"} {
							step := zero
							step.Args = []any{left, "cat"}
							fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
						}
					}()
				}
			}
		}
	})
}

func TestReferenceFTSMatchResolution(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range []struct {
			expression string
			want       bool
		}{
			{"'cat' @@ 'cat | rat'", false}, {"'cat'::tsvector @@ 'cat | rat'", true},
			{"'cat | rat' @@ 'cat'::tsvector", true}, {"'cat'::text @@ 'cat | rat'", false},
			{"'Rats' @@ 'rat'::tsquery", true}, {"'Rats'::tsvector @@ 'rat'::tsquery", false},
			{"'cat'::tsvector OPERATOR(pg_catalog.@@) 'cat'::tsquery", true},
			{"'{\"a\":1}'::jsonb @@ '$.a == 1'::jsonpath", true},
		} {
			run(ftsMatchStep("SELECT "+tc.expression, "?column?", [][]any{{tc.want}}))
		}
		for _, tc := range []struct {
			expression string
			args       []any
			want       bool
		}{
			{"$1 @@ $2", []any{"cat", "cat | rat"}, false},
			{"$1::tsvector @@ $2", []any{"cat", "cat | rat"}, true},
			{"$1 @@ $2::tsvector", []any{"cat | rat", "cat"}, true},
			{"$1::text @@ $2", []any{"cat", "cat | rat"}, false},
			{"$1 @@ $2::tsquery", []any{"Rats", "rat"}, true},
		} {
			run(ftsMatchStep("SELECT "+tc.expression, "?column?", [][]any{{tc.want}}, tc.args...))
		}
		for _, typ := range []string{"tsvector", "tsquery", "text"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN match_"+typ+" AS "+typ, 0))
		}
		for _, signature := range ftsMatchSignatures {
			run(ftsMatchStep("SELECT $1::match_"+signature.left+" @@ $2::match_"+signature.right, "?column?", [][]any{{true}}, "cat", "cat"))
		}
	})
}

func TestReferenceFTSMatchDiagnostics(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			sql, signature string
			position       int
		}{
			{"SELECT 1 @@ 2", "integer @@ integer", 10},
			{"SELECT 'a'::tsquery @@ 'b'::text", "tsquery @@ text", 21},
			{"SELECT 'a'::tsvector @@ 'b'::text", "tsvector @@ text", 22},
			{"SELECT 'a'::tsquery @@ 'b'::tsquery", "tsquery @@ tsquery", 21},
			{"SELECT 'a'::tsvector @@ 'b'::tsvector", "tsvector @@ tsvector", 22},
			{"SELECT 1 OPERATOR(pg_catalog.@@) 2", "integer pg_catalog.@@ integer", 10},
			{"/*é*/ SELECT 1 @@ 2", "integer @@ integer", 16},
			{"SELECT NULL::tsquery @@ 'b'::text", "tsquery @@ text", 22},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
			d.Position = fixture.Position(tc.position)
			d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			ftsOpsDiagnosticRoutes(t, p, b, tc.sql, d, true)
		}
		// Preparing the outer SQL utility does not analyze its enclosed query.
		prepare := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: integer @@ integer")
		prepare.Position = "31"
		prepare.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		ftsOpsDiagnosticRoutes(t, p, b, "PREPARE match_bad AS SELECT 1 @@ 2", prepare, false)
		for _, tc := range []struct{ sql, signature string }{
			{"SELECT ts_match_vq()", "ts_match_vq()"},
			{"SELECT ts_match_vq('cat')", "ts_match_vq(unknown)"},
			{"SELECT ts_match_vq(1,2)", "ts_match_vq(integer, integer)"},
			{"SELECT ts_match_qv('cat'::text,'cat'::text)", "ts_match_qv(text, text)"},
			{"SELECT ts_match_tt('cat','cat','cat')", "ts_match_tt(unknown, unknown, unknown)"},
			{"SELECT ts_match_tq(1,2)", "ts_match_tq(integer, integer)"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Position = "8"
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			ftsOpsDiagnosticRoutes(t, p, b, tc.sql, d, true)
		}
		literal := "SELECT NULL::tsvector @@ 'a &'::tsquery"
		d := fixture.ControlsReferenceDiagnostic("42601", `no operand in tsquery: "a &"`)
		d.Position = fixture.Position(strings.Index(literal, "'a &'") + 1)
		ftsOpsDiagnosticRoutes(t, p, b, literal, d, true)
		query := "SELECT $1::tsvector @@ $2::tsquery"
		d.Position = ""
		d.Context = "unnamed portal parameter $2 = '...'"
		ftsOpsDiagnosticRoutes(t, p, b, query, d, false, nil, "a &")
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		for range 2 {
			s := fixture.ErrorDiagnostic(query, d, "cat", "a &")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsMatchStep(query, "?column?", [][]any{{true}}, "cat", "cat"))
	})
}
