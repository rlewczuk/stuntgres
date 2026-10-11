package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceViewCheckGrammar(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
		for i, clause := range []string{"WITH CHECK OPTION", "WITH LOCAL CHECK OPTION", "WITH CASCADED CHECK OPTION"} {
			name := fmt.Sprintf("v%d", i)
			run(fixture.Exec("CREATE VIEW "+name+" AS SELECT a,b FROM t WHERE a<b "+clause, 0), viewCheckError("INSERT INTO "+name+" VALUES(2,1)", name, "2, 1"), fixture.Exec("INSERT INTO "+name+" VALUES(1,2)", 1))
		}
		run(fixture.Exec("CREATE TEMP VIEW temp_v AS SELECT a,b FROM t WHERE a<b WITH LOCAL CHECK OPTION", 0), viewCheckError("INSERT INTO temp_v VALUES(3,2)", "temp_v", "3, 2"))
		for _, tc := range []struct{ q, token string }{
			{"CREATE VIEW bad AS SELECT /* café */ a FROM t WITH LOCAL CHECK", ""},
			{"CREATE VIEW bad AS SELECT /* café */ a FROM t WITH CHECK OPTION garbage", "garbage"},
		} {
			pos := utf8.RuneCountInString(tc.q) + 1
			message := "syntax error at end of input"
			if tc.token != "" {
				pos = utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1
				message = "syntax error at or near \"" + tc.token + "\""
			}
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: message, Position: strconv.Itoa(pos)})
			s.Prepared = held
			s.ErrorStage = "execute"
			if held {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		run(viewCheckPairRows([]any{int64(1), int64(2)}, []any{int64(1), int64(2)}, []any{int64(1), int64(2)}))
	})
}

func TestReferenceViewCheckDeclaration(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
		for _, tc := range []struct{ query, hint string }{
			{"SELECT DISTINCT a FROM t", "Views containing DISTINCT are not automatically updatable."},
			{"SELECT a FROM t GROUP BY a", "Views containing GROUP BY are not automatically updatable."},
			{"SELECT sum(a) FROM t", "Views that return aggregate functions are not automatically updatable."},
			{"SELECT t.a FROM t JOIN t AS other ON t.a=other.a", "Views that do not select from a single table or view are not automatically updatable."},
			{"SELECT a+1 AS a FROM t", "Views that have no updatable columns are not automatically updatable."},
			{"SELECT FROM t", "Views that have no updatable columns are not automatically updatable."},
		} {
			for _, q := range []string{"CREATE VIEW bad AS " + tc.query + " WITH CHECK OPTION", "CREATE OR REPLACE VIEW bad AS " + tc.query + " WITH LOCAL CHECK OPTION"} {
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "WITH CHECK OPTION is supported only on automatically updatable views", Hint: tc.hint}))
			}
			run(fixture.Exec("CREATE VIEW bad AS "+tc.query, 0), fixture.ImplicitError("ALTER VIEW bad SET(check_option=local)", fixture.Diagnostic{Code: "0A000", Message: "WITH CHECK OPTION is supported only on automatically updatable views", Hint: tc.hint}), fixture.Exec("DROP VIEW bad", 0))
		}
		run(fixture.Exec("CREATE VIEW mixed AS SELECT a,a+1 AS calc FROM t WHERE a>0 WITH LOCAL CHECK OPTION", 0), fixture.Exec("INSERT INTO mixed(a) VALUES(1)", 1), viewCheckError("INSERT INTO mixed(a) VALUES(-1)", "mixed", "-1, null"))
		run(fixture.Exec("CREATE VIEW restricted AS SELECT DISTINCT a FROM t", 0), fixture.Exec("CREATE VIEW outer_v AS SELECT a FROM restricted WITH CHECK OPTION", 0))
		s := fixture.ImplicitError("INSERT INTO outer_v VALUES(2)", fixture.Diagnostic{Code: "55000", Message: "cannot insert into view \"restricted\"", Detail: "Views containing DISTINCT are not automatically updatable.", Hint: "To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule."})
		s.ErrorStage = "execute"
		if held {
			s.ErrorStage = "prepare"
		}
		run(s)
	})
}

func TestReferenceViewCheckOptions(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0), fixture.Exec("CREATE VIEW v WITH(security_barrier=true,security_invoker=true,check_option='LoCaL') AS SELECT a,b FROM t WHERE a<b", 0))
		enum := fixture.Diagnostic{Code: "22023", Message: "invalid value for enum option \"check_option\": here", Detail: "Valid values are \"local\" and \"cascaded\"."}
		shape := fixture.Diagnostic{Code: "0A000", Message: "WITH CHECK OPTION is supported only on automatically updatable views", Hint: "Views containing DISTINCT are not automatically updatable."}
		run(fixture.ImplicitError("CREATE VIEW bad WITH(check_option=here) AS SELECT DISTINCT a FROM t", shape), fixture.Exec("CREATE VIEW restricted AS SELECT DISTINCT a FROM t", 0), fixture.ImplicitError("ALTER VIEW restricted SET(check_option=here)", enum), fixture.Exec("DROP VIEW restricted", 0))
		run(fixture.ImplicitError("CREATE OR REPLACE VIEW v WITH(check_option=here) AS SELECT a AS renamed,b FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change name of view column \"a\" to \"renamed\"", Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."}))
		for _, value := range []string{"here", "loc", "' local '", "true"} {
			text := strings.Trim(value, "'")
			d := fixture.Diagnostic{Code: "22023", Message: "invalid value for enum option \"check_option\": " + text, Detail: "Valid values are \"local\" and \"cascaded\"."}
			run(fixture.ImplicitError("ALTER VIEW v SET(check_option="+value+")", d), fixture.ImplicitError("CREATE VIEW bad WITH(check_option="+value+") AS SELECT a FROM t", d))
		}
		run(fixture.ImplicitError("CREATE VIEW bad WITH(check_option) AS SELECT a FROM t", fixture.Diagnostic{Code: "22023", Message: "invalid value for enum option \"check_option\": true", Detail: "Valid values are \"local\" and \"cascaded\"."}))
		for _, q := range []string{"CREATE VIEW bad WITH(check_option=local,check_option=cascaded) AS SELECT a FROM t", "CREATE VIEW bad WITH(check_option=local) AS SELECT a FROM t WITH CASCADED CHECK OPTION", "ALTER VIEW v SET(check_option=local,check_option=cascaded)"} {
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "parameter \"check_option\" specified more than once"}))
		}
		run(viewCheckError("INSERT INTO v VALUES(2,1)", "v", "2, 1"), fixture.Exec("ALTER VIEW v RESET(check_option)", 0), fixture.Exec("INSERT INTO v VALUES(2,1)", 1), fixture.Exec("ALTER VIEW v SET(check_option='CASCADED')", 0), viewCheckError("INSERT INTO v VALUES(3,1)", "v", "3, 1"), fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT a,b FROM t WHERE a<b", 0), fixture.Exec("INSERT INTO v VALUES(3,1)", 1), fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT a,b FROM t WHERE a<b WITH LOCAL CHECK OPTION", 0), viewCheckError("INSERT INTO v VALUES(4,1)", "v", "4, 1"), viewCheckPairRows([]any{int64(2), int64(1)}, []any{int64(3), int64(1)}))
	})
}

func TestReferenceViewCheckDiagnostics(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT,secret TEXT DEFAULT 'hidden',flag BOOL DEFAULT false)", 0), fixture.Exec("CREATE VIEW v AS SELECT b AS y,a AS x FROM t WHERE a<b WITH CHECK OPTION", 0), viewCheckError("INSERT INTO v(x,y) VALUES(3,NULL)", "v", "3, null, hidden, f"))
		long := strings.Repeat("é", 40)
		run(viewCheckError("INSERT INTO v(x,y) VALUES(4,3)", "v", "4, 3, hidden, f"))
		run(fixture.Select("SELECT a,b,secret,flag FROM t", []string{"a", "b", "secret", "flag"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, nil))
		run(fixture.Exec("CREATE VIEW text_v AS SELECT a,b,secret,flag FROM t WHERE a<b WITH CHECK OPTION", 0), viewCheckError("INSERT INTO text_v VALUES(9,1,$1,true)", "text_v", "9, 1, "+strings.Repeat("é", 32)+"..., t", long))
		q := "CREATE VIEW bad AS SELECT /* café */ missing FROM t WITH CHECK OPTION"
		run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1)}))
	})
}
