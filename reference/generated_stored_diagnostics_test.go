package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func generatedStoredPosition(q, token string) string {
	return fmt.Sprint(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
}

func TestReferenceGeneratedStoredDefinitionDiagnostics(t *testing.T) {
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
				generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
					q := fmt.Sprintf(`/*é*/ CREATE TABLE g(a INT,b %s GENERATED ALWAYS AS(%s) STORED)`, tc.typ, tc.expr)
					position := ""
					if tc.token != "" {
						position = generatedStoredPosition(q, tc.token)
						if tc.name == "whole-row" {
							position = fmt.Sprint(utf8.RuneCountInString(q[:strings.Index(q, "AS(g)")]) + 4)
						}
					}
					s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint, Position: position})
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
					run(referenceImplicitError(`SELECT * FROM g`, referenceDiagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
				})
			})
		}
	}
	for _, expr := range []string{"c+1", "CASE WHEN FALSE THEN c ELSE a END"} {
		t.Run("other-generated/"+expr, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
				q := `CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(` + expr + `) STORED,c INT GENERATED ALWAYS AS(a+1) STORED)`
				run(referenceImplicitError(q, referenceDiagnostic{Code: "42P17", Message: `cannot use generated column "c" in column generation expression`, Detail: "A generated column cannot reference another generated column.", Position: generatedStoredPosition(q, "c")}))
			})
		})
	}
}

func TestReferenceGeneratedStoredDeclarationDiagnostics(t *testing.T) {
	for _, tc := range []struct{ clauses, message, token string }{
		{`GENERATED ALWAYS AS(a+1) STORED GENERATED ALWAYS AS(a+2) STORED`, `multiple generation clauses specified for column "b" of table "g"`, `GENERATED ALWAYS AS(a+2)`},
		{`DEFAULT 1 GENERATED ALWAYS AS(a+1) STORED`, `both default and generation expression specified for column "b" of table "g"`, `GENERATED`},
		{`GENERATED ALWAYS AS(a+1) STORED DEFAULT 1`, `both default and generation expression specified for column "b" of table "g"`, `DEFAULT`},
		{`GENERATED ALWAYS AS IDENTITY GENERATED ALWAYS AS(a+1) STORED`, `both identity and generation expression specified for column "b" of table "g"`, `GENERATED ALWAYS AS(a+1)`},
		{`GENERATED ALWAYS AS(a+1) STORED GENERATED ALWAYS AS IDENTITY`, `both identity and generation expression specified for column "b" of table "g"`, `GENERATED ALWAYS AS IDENTITY`},
		{`GENERATED BY DEFAULT AS(a+1) STORED`, `for a generated column, GENERATED ALWAYS must be specified`, `BY DEFAULT`},
	} {
		t.Run(tc.clauses, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
				q := `CREATE TABLE g(a INT,b INT ` + tc.clauses + `)`
				run(referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: tc.message, Position: generatedStoredPosition(q, tc.token)}))
				run(referenceImplicitError(`SELECT * FROM g`, referenceDiagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
			})
		})
	}
}

func TestReferenceGeneratedStoredWriteDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		q      string
		args   []any
		update bool
	}{
		{`INSERT INTO g VALUES(2,3)`, nil, false}, {`INSERT INTO g VALUES(2,NULL)`, nil, false}, {`INSERT INTO g VALUES(2,$1)`, []any{3}, false},
		{`INSERT INTO g VALUES(2,1+2)`, nil, false}, {`INSERT INTO g SELECT 2,3 FROM g WHERE FALSE`, nil, false},
		{`INSERT INTO g OVERRIDING SYSTEM VALUE VALUES(2,3)`, nil, false}, {`INSERT INTO g OVERRIDING USER VALUE VALUES(2,3)`, nil, false},
		{`UPDATE g SET b=3 WHERE FALSE`, nil, true}, {`UPDATE g SET b=NULL`, nil, true}, {`UPDATE g SET b=$1`, []any{3}, true}, {`UPDATE g SET b=b`, nil, true},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.q, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
					run(referenceExec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
					run(referenceExec(`INSERT INTO g(a) VALUES(1)`, 1))
					message := `cannot insert a non-DEFAULT value into column "b"`
					if tc.update {
						message = `column "b" can only be updated to DEFAULT`
					}
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "428C9", Message: message, Detail: `Column "b" is a generated column.`}, tc.args...)
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				})
			})
		}
	}
}

func TestReferenceGeneratedStoredPlanning(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE g(a INT,b DOUBLE PRECISION GENERATED ALWAYS AS(CASE WHEN FALSE THEN random() ELSE a END) STORED)`, 0))
		run(referenceExec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(2), float64(2)}}))
		run(referenceExec(`CREATE TABLE deferred(a INT,b INT GENERATED ALWAYS AS(a/0) STORED)`, 0))
		run(referenceImplicitError(`INSERT INTO deferred(a) VALUES(1)`, referenceDiagnostic{Code: "22012", Message: "division by zero"}))
		run(referenceSelect(`SELECT * FROM deferred`, []string{"a", "b"}, []string{"INT4", "INT4"}, nil))
	})
}
