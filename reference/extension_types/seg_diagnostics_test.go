package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSegDiagnostics(t *testing.T) {
	cases := []struct{ input, code, message, detail string }{
		{"", "42601", "bad seg representation", "syntax error at end of input"},
		{"ABC", "42601", "bad seg representation", `syntax error at or near "A"`},
		{"1ABC", "42601", "bad seg representation", `syntax error at or near "A"`},
		// REL_18_6 segscan.l surrounds yytext with quotes without escaping it.
		{`\`, "42601", "bad seg representation", `syntax error at or near "\"`},
		{`"`, "42601", "bad seg representation", `syntax error at or near """`},
		{"1.", "42601", "bad seg representation", `syntax error at or near "."`},
		{".1", "42601", "bad seg representation", `syntax error at or near "."`},
		{"1..2.", "42601", "bad seg representation", `syntax error at or near "."`},
		{"1.....", "42601", "bad seg representation", `syntax error at or near ".."`},
		{"1 e7", "42601", "bad seg representation", `syntax error at or near "e"`},
		{"1e700", "22003", `"1e700" is out of range for type real`, ""},
		{"5 .. 2", "22023", "swapped boundaries: 5 is greater than 2", ""},
		{"1e-46", "22003", `"1e-46" is out of range for type real`, ""},
		{"NaN", "42601", "bad seg representation", `syntax error at or near "N"`},
		{"Infinity", "42601", "bad seg representation", `syntax error at or near "I"`},
		{"[1,2]", "42601", "bad seg representation", `syntax error at or near "["`},
		{"..", "42601", "bad seg representation", "syntax error at end of input"},
		{"1(+-)", "42601", "bad seg representation", "syntax error at end of input"},
		{"1(+-)~2", "42601", "bad seg representation", `syntax error at or near "~"`},
		{"1e+", "42601", "bad seg representation", `syntax error at or near "e"`},
		{"1 234", "42601", "bad seg representation", `syntax error at or near "234"`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE seg_diag(id integer NOT NULL,v seg)", 0))
				run(fixture.Exec("INSERT INTO seg_diag VALUES (1,'1.00'),(2,'~1.5 .. 2.5')", 2))
				unchanged := func() {
					run(segSelect(b, "SELECT id,v FROM seg_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1.00"}, {int64(2), "~1.5 .. 2.5"}}))
				}
				for _, prepared := range []bool{false, true} {
					for _, q := range []struct {
						sql, pos string
						rows     bool
					}{
						{"SELECT " + queryIntLiteral(tc.input) + "::seg AS seg;", "8", true},
						{"/*é*/ SELECT " + queryIntLiteral(tc.input) + "::seg AS seg;", "14", true},
						{"INSERT INTO seg_diag VALUES (3," + queryIntLiteral(tc.input) + ")", "32", false},
						{"INSERT INTO seg_diag VALUES (3,'3'),(4," + queryIntLiteral(tc.input) + ")", "40", false},
					} {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Detail = tc.detail
						want.Position = q.pos
						queryIntError(t, p, b, c, q.sql, prepared, q.rows, true, want)
						unchanged()
					}
					for _, q := range []struct {
						sql       string
						parameter int
						args      []any
					}{
						{"INSERT INTO seg_diag VALUES (3,$1)", 1, []any{tc.input}},
						{"INSERT INTO seg_diag VALUES (3,$1),(4,$2)", 2, []any{"3", tc.input}},
						{"UPDATE seg_diag SET v=CASE WHEN id=1 THEN $1::seg ELSE $2::seg END", 2, []any{"3", tc.input}},
					} {
						want := queryIntParameterDiagnostic(tc.code, tc.message, tc.input, q.parameter)
						want.Detail = tc.detail
						queryIntError(t, p, b, c, q.sql, prepared, false, false, want, q.args...)
						unchanged()
					}
				}
			})
		})
	}
}

func TestReferenceSegInstallation(t *testing.T) {
	queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := fixture.ControlsReferenceDiagnostic("42704", `type "seg" does not exist`)
		want.Position = "27"
		queryIntError(t, p, b, c, "CREATE TABLE seg_absent(v seg)", false, false, false, want)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION seg", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='seg'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION seg", 0))
		run(fixture.Exec("CREATE TABLE seg_install(v public.seg)", 0))
		run(fixture.Select("SELECT extname,extversion,extrelocatable FROM pg_extension WHERE extname='seg'", []string{"extname", "extversion", "extrelocatable"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"seg", "1.4", true}}))
		want = fixture.ControlsReferenceDiagnostic("42710", `extension "seg" already exists`)
		for _, prepared := range []bool{false, true} {
			queryIntError(t, p, b, c, "CREATE EXTENSION seg", prepared, false, false, want)
		}
		var notices []fixture.Diagnostic
		_, nc, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, r, fixture.Exec("CREATE EXTENSION IF NOT EXISTS seg", 0))
		expected := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "seg" already exists, skipping`}
		if len(notices) != 1 || notices[0] != expected {
			t.Fatalf("notices %#v want %#v", notices, expected)
		}
	})
}

func TestReferenceSegInstallationConflict(t *testing.T) {
	queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TYPE seg AS ENUM ('occupied')", 0))
		want := fixture.ControlsReferenceDiagnostic("42710", `type "seg" already exists`)
		want.Context = "SQL statement \"CREATE TYPE seg (\n\tINTERNALLENGTH = 12,\n\tINPUT = seg_in,\n\tOUTPUT = seg_out\n)\"\nextension script file \"seg--1.1.sql\", near line 18"
		queryIntError(t, p, b, c, "CREATE EXTENSION seg", false, false, false, want)
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='seg'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('seg_in','seg_out') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT enumlabel FROM pg_enum WHERE enumtypid=('seg'::regtype)::oid", []string{"enumlabel"}, []string{"NAME"}, [][]any{{"occupied"}}))
	})
}
