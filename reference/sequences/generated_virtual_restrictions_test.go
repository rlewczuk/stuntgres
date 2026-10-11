package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceGeneratedVirtualDefinitionDiagnostics(t *testing.T) {
	cases := []struct{ name, expr, typ, code, message, detail, hint, token string }{
		{"self", "b+1", "INT", "42P17", `cannot use generated column "b" in column generation expression`, `A generated column cannot reference another generated column.`, "", "b+1"},
		{"dead-self", "CASE WHEN FALSE THEN b ELSE a END", "INT", "42P17", `cannot use generated column "b" in column generation expression`, `A generated column cannot reference another generated column.`, "", "b ELSE"},
		{"cast-self", "CAST(b AS INTEGER)", "INT", "42P17", `cannot use generated column "b" in column generation expression`, `A generated column cannot reference another generated column.`, "", "b AS"},
		{"missing", "c+1", "INT", "42703", `column "c" does not exist`, "", "", "c+1"},
		{"system", "xmin", "BIGINT", "42P10", `cannot use system column "xmin" in column generation expression`, "", "", "xmin"},
		{"whole-row", "g", "INT", "42P17", "cannot use whole-row variable in column generation expression", "This would cause the generated column to depend on its own value.", "", "AS(g)"},
		{"volatile", "random()", "DOUBLE PRECISION", "42P17", "generation expression is not immutable", "", "", ""},
		{"stable", "current_user", "TEXT", "42P17", "generation expression is not immutable", "", "", ""},
		{"aggregate", "sum(a)", "BIGINT", "42803", "aggregate functions are not allowed in column generation expressions", "", "", "sum"},
		{"grouping", "grouping(a)", "INT", "42803", "grouping operations are not allowed in column generation expressions", "", "", "grouping"},
		{"window", "row_number() OVER ()", "BIGINT", "42P20", "window functions are not allowed in column generation expressions", "", "", "row_number"},
		{"subquery", "(SELECT a)", "INT", "0A000", "cannot use subquery in column generation expression", "", "", "(SELECT"},
		{"set-returning", "generate_series(1,a)", "INT", "0A000", "set-returning functions are not allowed in column generation expressions", "", "", "generate_series"},
		{"mismatch", "TRUE", "INT", "42804", `column "b" is of type integer but default expression is of type boolean`, "", "You will need to rewrite or cast the expression.", ""},
		{"constant-zero", "1/0", "INT", "22012", "division by zero", "", "", ""},
		{"parameter", "$1", "INT", "42P02", "there is no parameter $1", "", "", "$1"},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
					q := fmt.Sprintf(`/*é*/ CREATE TABLE g(a INT,b %s GENERATED ALWAYS AS(%s) VIRTUAL)`, tc.typ, tc.expr)
					position := ""
					if tc.token != "" {
						position = fixture.GeneratedStoredPosition(q, tc.token)
						if tc.name == "whole-row" {
							position = fmt.Sprint(utf8.RuneCountInString(q[:strings.Index(q, "AS(g)")]) + 4)
						}
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint, Position: position})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					run(fixture.ImplicitError(`SELECT * FROM g`, fixture.Diagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
					run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
				})
			})
		}
	}
	for _, expr := range []string{"c+1", "CASE WHEN FALSE THEN c ELSE a END"} {
		t.Run("other-generated/"+expr, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				q := `CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(` + expr + `) VIRTUAL,c INT GENERATED ALWAYS AS(a+1) VIRTUAL)`
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: `cannot use generated column "c" in column generation expression`, Detail: "A generated column cannot reference another generated column.", Position: fixture.GeneratedStoredPosition(q, "c")}))
			})
		})
	}
}

func TestReferenceGeneratedVirtualDeclarationDiagnostics(t *testing.T) {
	for _, tc := range []struct{ clauses, message, token string }{
		{`GENERATED ALWAYS AS(a+1) VIRTUAL GENERATED ALWAYS AS(a+2) VIRTUAL`, `multiple generation clauses specified for column "b" of table "g"`, `GENERATED ALWAYS AS(a+2)`},
		{`DEFAULT 1 GENERATED ALWAYS AS(a+1) VIRTUAL`, `both default and generation expression specified for column "b" of table "g"`, `GENERATED`},
		{`GENERATED ALWAYS AS(a+1) VIRTUAL DEFAULT 1`, `both default and generation expression specified for column "b" of table "g"`, `DEFAULT`},
		{`GENERATED ALWAYS AS IDENTITY GENERATED ALWAYS AS(a+1) VIRTUAL`, `both identity and generation expression specified for column "b" of table "g"`, `GENERATED ALWAYS AS(a+1)`},
		{`GENERATED ALWAYS AS(a+1) VIRTUAL GENERATED ALWAYS AS IDENTITY`, `both identity and generation expression specified for column "b" of table "g"`, `GENERATED ALWAYS AS IDENTITY`},
		{`GENERATED BY DEFAULT AS(a+1) VIRTUAL`, `for a generated column, GENERATED ALWAYS must be specified`, `BY DEFAULT`},
	} {
		t.Run(tc.clauses, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				q := `CREATE TABLE g(a INT,b INT ` + tc.clauses + `)`
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: fixture.GeneratedStoredPosition(q, tc.token)}))
				run(fixture.ImplicitError(`SELECT * FROM g`, fixture.Diagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
			})
		})
	}
}

func TestReferenceGeneratedVirtualWrites(t *testing.T) {
	for _, tc := range []struct {
		q      string
		args   []any
		update bool
	}{
		{`INSERT INTO g VALUES(2,3)`, nil, false}, {`INSERT INTO g VALUES(2,NULL)`, nil, false}, {`INSERT INTO g VALUES(2,$1)`, []any{3}, false},
		{`INSERT INTO g VALUES(2,1+2)`, nil, false}, {`INSERT INTO g SELECT 2,3 FROM g WHERE FALSE`, nil, false},
		{`INSERT INTO g OVERRIDING SYSTEM VALUE VALUES(2,3)`, nil, false}, {`INSERT INTO g OVERRIDING USER VALUE VALUES(2,3)`, nil, false},
		{`INSERT INTO g VALUES(7,DEFAULT),(8,9)`, nil, false},
		{`UPDATE g SET b=3 WHERE FALSE`, nil, true}, {`UPDATE g SET b=NULL`, nil, true}, {`UPDATE g SET b=$1`, []any{3}, true}, {`UPDATE g SET b=b`, nil, true},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.q, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
					run(fixture.Exec(`INSERT INTO g(a) VALUES(1)`, 1))
					message := `cannot insert a non-DEFAULT value into column "b"`
					if tc.update {
						message = `column "b" can only be updated to DEFAULT`
					}
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "428C9", Message: message, Detail: `Column "b" is a generated column.`}, tc.args...)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Select(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				})
			})
		}
	}
}

func TestReferenceGeneratedVirtualMixedReferences(t *testing.T) {
	for _, kind := range []string{"STORED", "VIRTUAL"} {
		generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			q := `CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) ` + kind + `,c INT GENERATED ALWAYS AS(b+1) VIRTUAL)`
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: `cannot use generated column "b" in column generation expression`, Detail: "A generated column cannot reference another generated column.", Position: fixture.GeneratedStoredPosition(q, "b+1")}))
			q = `CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL,c INT GENERATED ALWAYS AS(b+1) STORED)`
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: `cannot use generated column "b" in column generation expression`, Detail: "A generated column cannot reference another generated column.", Position: fixture.GeneratedStoredPosition(q, "b+1")}))
		})
	}
}
