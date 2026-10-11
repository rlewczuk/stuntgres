package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterGeneratedExpressionDiagnostics(t *testing.T) {
	cases := []struct{ name, expr, typ, code, message, detail, hint string }{
		{"self", "b+1", "INT", "42P17", `cannot use generated column "b" in column generation expression`, "A generated column cannot reference another generated column.", ""},
		{"dead-self", "CASE WHEN FALSE THEN b ELSE a END", "INT", "42P17", `cannot use generated column "b" in column generation expression`, "A generated column cannot reference another generated column.", ""},
		{"other", "c+1", "INT", "42P17", `cannot use generated column "c" in column generation expression`, "A generated column cannot reference another generated column.", ""},
		{"missing", "missing+1", "INT", "42703", `column "missing" does not exist`, "", ""},
		{"system", "xmin", "BIGINT", "42P10", `cannot use system column "xmin" in column generation expression`, "", ""},
		{"whole-row", "g", "INT", "42P17", "cannot use whole-row variable in column generation expression", "This would cause the generated column to depend on its own value.", ""},
		{"volatile", "random()", "DOUBLE PRECISION", "42P17", "generation expression is not immutable", "", ""},
		{"stable", "current_user", "TEXT", "42P17", "generation expression is not immutable", "", ""},
		{"aggregate", "sum(a)", "BIGINT", "42803", "aggregate functions are not allowed in column generation expressions", "", ""},
		{"grouping", "grouping(a)", "INT", "42803", "grouping operations are not allowed in column generation expressions", "", ""},
		{"window", "row_number() OVER ()", "BIGINT", "42P20", "window functions are not allowed in column generation expressions", "", ""},
		{"subquery", "(SELECT a)", "INT", "0A000", "cannot use subquery in column generation expression", "", ""},
		{"srf", "generate_series(1,a)", "INT", "0A000", "set-returning functions are not allowed in column generation expressions", "", ""},
		{"parameter", "$1", "INT", "42P02", "there is no parameter $1", "", ""},
		{"mismatch", "TRUE", "INT", "42804", `column "b" is of type integer but default expression is of type boolean`, "", "You will need to rewrite or cast the expression."},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec(`CREATE TABLE g(a INT,b `+tc.typ+` GENERATED ALWAYS AS(a+1) STORED,c INT GENERATED ALWAYS AS(a+2) STORED)`, 0))
					q := `/*é*/ ALTER TABLE g ALTER COLUMN b SET EXPRESSION AS(` + tc.expr + `)`
					// ALTER utility analysis reports these semantic errors without a source Position.
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("targets/prepared=%v", prepared), func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED,id INT GENERATED ALWAYS AS IDENTITY)`, 0))
				for _, tc := range []struct{ q, code, message string }{
					{`ALTER TABLE g ALTER a SET EXPRESSION AS(1)`, "55000", `column "a" of relation "g" is not a generated column`},
					{`ALTER TABLE g ALTER id SET EXPRESSION AS(1)`, "55000", `column "id" of relation "g" is not a generated column`},
					{`ALTER TABLE g ALTER a DROP EXPRESSION`, "55000", `column "a" of relation "g" is not a generated column`},
					{`ALTER TABLE g ALTER missing DROP EXPRESSION IF EXISTS`, "42703", `column "missing" of relation "g" does not exist`},
					{`ALTER TABLE g ALTER missing SET EXPRESSION AS(1)`, "42703", `column "missing" of relation "g" does not exist`},
					{`ALTER TABLE g ALTER tableoid SET EXPRESSION AS(1)`, "0A000", `cannot alter system column "tableoid"`},
					{`ALTER TABLE g ALTER tableoid DROP EXPRESSION`, "0A000", `cannot alter system column "tableoid"`},
				} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			})
		})
	}
	for _, tc := range []struct{ tail, token string }{
		{`SET EXPRESSION (a+2)`, "("}, {`SET EXPRESSION AS a+2`, "a+2"}, {`SET EXPRESSION AS(a+2) STORED`, "STORED"}, {`SET EXPRESSION AS(a+2) USING a`, "USING"}, {`DROP IF EXISTS EXPRESSION`, "IF"}, {`SET EXPRESSION IF EXISTS AS(a+2)`, "IF"}, {`SET EXPRESSION AS(a+2), ALTER b SET EXPRESSION AS`, ""},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("syntax=%s/prepared=%v", tc.tail, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
					run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
					q := `/*é*/ ALTER TABLE g ALTER b ` + tc.tail
					message := `syntax error at or near "` + tc.token + `"`
					position := fixture.GeneratedStoredPosition(q, tc.token)
					if tc.token == "a+2" {
						message = `syntax error at or near "a"`
					}
					if tc.token == "" {
						message = "syntax error at end of input"
						position = fmt.Sprint(len([]rune(q)) + 1)
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: message, Position: position})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(3)}))
				})
			})
		}
	}
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused func(fixture.Step)) {
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`ALTER TABLE g ALTER a DROP EXPRESSION IF EXISTS`, 0))
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "a" of relation "g" is not a generated column, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices=%#v want=%#v", notices, want)
		}
		notices = nil
		run(fixture.Exec(`ALTER TABLE IF EXISTS absent_g ALTER missing SET EXPRESSION AS(1/0)`, 0))
		want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "absent_g" does not exist, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices=%#v want=%#v", notices, want)
		}
		run(fixture.Exec(`CREATE TABLE "g.é"("a.é" INT,"b.é" INT GENERATED ALWAYS AS("a.é"+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO "g.é"("a.é") VALUES(2)`, 1))
		run(fixture.Exec(`ALTER TABLE public."g.é" ALTER COLUMN "b.é" SET EXPRESSION AS("a.é"+2)`, 0))
		run(fixture.Select(`SELECT * FROM "g.é"`, []string{"a.é", "b.é"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}}))
	})
}
