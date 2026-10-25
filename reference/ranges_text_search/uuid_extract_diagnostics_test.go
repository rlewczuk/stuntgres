package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func extractDiagnostic(q, code, message, hint, at string, last bool) fixture.Diagnostic {
	d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint}
	if at != "" {
		i := strings.Index(q, at)
		if last {
			i = strings.LastIndex(q, at)
		}
		d.Position = strconv.Itoa(utf8.RuneCountInString(q[:i]) + 1)
	}
	return d
}

func TestReferenceUUIDExtractDiagnostics(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE extract_errors(v uuid,t text,n integer)", 0))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		type diagnosticCase struct {
			q, code, message, hint, at string
			last                       bool
		}
		for _, f := range []string{"uuid_extract_timestamp", "uuid_extract_version"} {
			cases := []diagnosticCase{}
			for _, arg := range []struct{ sql, typ string }{{"1", "integer"}, {"TRUE", "boolean"}, {"t", "text"}, {"", ""}, {"v,v", "uuid, uuid"}, {"uuid => v", "uuid => uuid"}, {"x => '" + extractA + "'", "x => unknown"}} {
				cases = append(cases, diagnosticCase{"SELECT " + f + "(" + arg.sql + ") FROM extract_errors", "42883", "function " + f + "(" + arg.typ + ") does not exist", hint, f, false})
			}
			cases = append(cases,
				diagnosticCase{"SELECT public." + f + "(v) FROM extract_errors", "42883", "function public." + f + "(uuid) does not exist", hint, "public", false},
				diagnosticCase{`SELECT "` + strings.ToUpper(f) + `"(v) FROM extract_errors`, "42883", "function " + strings.ToUpper(f) + "(uuid) does not exist", hint, `"` + strings.ToUpper(f) + `"`, false},
				diagnosticCase{"SELECT " + f + "(x => v,x => v) FROM extract_errors", "42601", `argument name "x" used more than once`, "", "x =>", true},
				diagnosticCase{"SELECT " + f + "(x => v,v) FROM extract_errors", "42601", "positional argument cannot follow named argument", "", "v)", false},
				diagnosticCase{"SELECT " + f + "(" + strings.TrimSuffix(strings.Repeat("v,", 101), ",") + ") FROM extract_errors", "54023", "cannot pass more than 100 arguments to a function", "", f, false},
			)
			for _, decoration := range []struct{ args, suffix, label, kind string }{
				{"DISTINCT v", "", "DISTINCT", "an aggregate function"},
				{"v ORDER BY 1", "", "ORDER BY", "an aggregate function"},
				{"v", " FILTER (WHERE true)", "FILTER", "an aggregate function"},
				{"v", " OVER ()", "OVER", "a window function nor an aggregate function"},
			} {
				cases = append(cases, diagnosticCase{"SELECT " + f + "(" + decoration.args + ")" + decoration.suffix + " FROM extract_errors", "42809", decoration.label + " specified, but " + f + " is not " + decoration.kind, "", f, false})
			}
			for _, prefix := range []string{"", "/*é*/ "} {
				cases = append(cases, diagnosticCase{prefix + "SELECT " + f + "('bad') FROM extract_errors", "22P02", `invalid input syntax for type uuid: "bad"`, "", "'bad'", false})
			}
			for _, args := range []string{"missing", "missing,v", "x => missing,x => v"} {
				cases = append(cases, diagnosticCase{"SELECT " + f + "(" + args + ") FILTER (WHERE missing_filter) FROM extract_errors", "42703", `column "missing" does not exist`, "", "missing", false})
			}
			for _, args := range []string{"v", "1", "", "x => v", strings.TrimSuffix(strings.Repeat("v,", 101), ",")} {
				cases = append(cases, diagnosticCase{"SELECT " + f + "(" + args + ") FILTER (WHERE missing_filter) FROM extract_errors", "42703", `column "missing_filter" does not exist`, "", "missing_filter", false})
			}
			cases = append(cases,
				diagnosticCase{"SELECT " + f + "(v) FILTER (WHERE n) FROM extract_errors", "42804", "argument of FILTER must be type boolean, not type integer", "", "n)", false},
				diagnosticCase{"SELECT " + f + "(1) FILTER (WHERE true)", "42883", "function " + f + "(integer) does not exist", hint, f, false},
			)
			for _, tc := range cases {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, extractDiagnostic(tc.q, tc.code, tc.message, tc.hint, tc.at, tc.last))
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
	})
}

func extractAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE uuid_extract_reader", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE uuid_extract_auth(v uuid)", 0))
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	var previous int64
	for _, contents := range []string{"", "NULL", "'" + extractA + "'"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM uuid_extract_auth", previous))
		previous = 0
		if contents != "" {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO uuid_extract_auth VALUES ("+contents+")", 1))
			previous = 1
		}
		for _, suffix := range []string{"", " WHERE false"} {
			q := "SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM uuid_extract_auth" + suffix
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION uuid_extract_reader", 0))
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table uuid_extract_auth"})
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			var want [][]any
			if suffix == "" && contents != "" {
				want = [][]any{{nil, nil}}
				if contents != "NULL" {
					want = [][]any{{extractTime, int64(1)}}
				}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, extractStep(q, want))
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE uuid_extract_auth", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP ROLE uuid_extract_reader", 0))
}

func TestReferenceUUIDExtractAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"uuid_extract_reader"})
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) { extractAuthorization(t, p, b) })
}
