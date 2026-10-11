package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceDefaultDiagnosticsAndConversionTiming(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{`CREATE TABLE bad (i integer DEFAULT TRUE)`, fixture.Diagnostic{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
				{`CREATE TABLE bad (i integer DEFAULT 'oops')`, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT '2147483648')`, fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT i)`, fixture.Diagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT $1)`, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT 1 DEFAULT 2)`, fixture.Diagnostic{Code: "42601", Message: `multiple default values specified for column "i" of table "bad"`, Position: "39"}},
			} {
				s := fixture.ImplicitError(tc.q, tc.want)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.ImplicitError(`SELECT * FROM bad`, fixture.Diagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
			}
			for _, tc := range []struct{ q, token, code, message string }{
				{`CREATE TABLE bad(i BOOL DEFAULT (TRUE OR i=1))`, "i=1", "0A000", "cannot use column reference in DEFAULT expression"},
				{`/*é*/ CREATE TABLE bad(i BOOL DEFAULT (FALSE AND $2))`, "$2", "42P02", "there is no parameter $2"},
			} {
				position := fmt.Sprint(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position})
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec(`CREATE TABLE narrowed(i integer DEFAULT 2147483648)`, 0))
			narrowing := fixture.ImplicitError(`INSERT INTO narrowed DEFAULT VALUES`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
			narrowing.ErrorStage = "execute"
			run(narrowing)
			run(fixture.Select(`SELECT * FROM narrowed`, []string{"i"}, []string{"INT4"}, nil))
			run(fixture.Exec(`CREATE TABLE sized(id INT,v NUMERIC(3,1) DEFAULT 12.34)`, 0))
			run(fixture.Exec(`INSERT INTO sized(id) VALUES(1)`, 1))
			run(fixture.Select(`SELECT * FROM sized`, []string{"id", "v"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "12.3"}}))
			run(fixture.Exec(`CREATE TABLE overflow(id INT,v NUMERIC(3,1) DEFAULT 99.95)`, 0))
			run(fixture.Exec(`INSERT INTO overflow VALUES(1,1)`, 1))
			overflow := fixture.ImplicitError(`INSERT INTO overflow(id) VALUES(2),(3)`, fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."})
			overflow.ErrorStage = "execute"
			run(overflow)
			run(fixture.Select(`SELECT * FROM overflow`, []string{"id", "v"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "1.0"}}))
			run(fixture.Exec(`CREATE TABLE nn(id INT,v INT DEFAULT NULL NOT NULL)`, 0))
			run(fixture.Exec(`INSERT INTO nn VALUES(1,9)`, 1))
			notNull := fixture.ImplicitError(`INSERT INTO nn VALUES(2,9),(3,DEFAULT)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Schema: "public", Table: "nn", Column: "v", Detail: "Failing row contains (3, null)."})
			notNull.ErrorStage = "execute"
			run(notNull)
			run(fixture.Select(`SELECT * FROM nn`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
			run(fixture.Exec(`CREATE TABLE plain_nn(id INT,v INT NOT NULL)`, 0))
			run(fixture.Exec(`INSERT INTO plain_nn VALUES(1,9)`, 1))
			plainNotNull := fixture.ImplicitError(`INSERT INTO plain_nn VALUES(2,9),(3,DEFAULT)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "plain_nn" violates not-null constraint`, Schema: "public", Table: "plain_nn", Column: "v", Detail: "Failing row contains (3, null)."})
			plainNotNull.ErrorStage = "execute"
			run(plainNotNull)
			run(fixture.Select(`SELECT * FROM plain_nn`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
		})
	}
}

func TestReferenceDefaultDiagnosticPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ name, query, code, message, hint, position string }{
			{"boolean-column", `CREATE TABLE bad(b BOOL DEFAULT (1 AND b))`, "42804", "argument of AND must be type boolean, not type integer", "", "34"},
			{"boolean-parameter", `CREATE TABLE bad(b BOOL DEFAULT (1 AND $1))`, "42804", "argument of AND must be type boolean, not type integer", "", "34"},
			{"input-column", `CREATE TABLE bad(b BOOL DEFAULT ('oops' AND b))`, "22P02", `invalid input syntax for type boolean: "oops"`, "", "34"},
			{"input-parameter", `CREATE TABLE bad(b BOOL DEFAULT ('oops' AND $1))`, "22P02", `invalid input syntax for type boolean: "oops"`, "", "34"},
			{"operator-column", `CREATE TABLE bad(b BOOL DEFAULT ((1 = TRUE) AND b))`, "42883", "operator does not exist: integer = boolean", "No operator matches the given name and argument types. You might need to add explicit type casts.", "37"},
			{"operator-parameter", `CREATE TABLE bad(b BOOL DEFAULT ((1 = TRUE) AND $1))`, "42883", "operator does not exist: integer = boolean", "No operator matches the given name and argument types. You might need to add explicit type casts.", "37"},
			{"comparison-input-column", `CREATE TABLE bad(b BOOL DEFAULT (('oops' = 1) AND b))`, "22P02", `invalid input syntax for type integer: "oops"`, "", "35"},
			{"comparison-input-parameter", `CREATE TABLE bad(b BOOL DEFAULT (('oops' = 1) AND $1))`, "22P02", `invalid input syntax for type integer: "oops"`, "", "35"},
			{"column-before-input", `CREATE TABLE bad(b BOOL DEFAULT (b AND 'oops'))`, "0A000", "cannot use column reference in DEFAULT expression", "", "34"},
			{"parameter-before-input", `CREATE TABLE bad(b BOOL DEFAULT ($1 AND 'oops'))`, "42P02", "there is no parameter $1", "", "34"},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.name), func(t *testing.T) {
				p := fixture.NewScenario(t)
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position})
				s.Prepared, s.ErrorStage = prepared, "execute"
				p.Step(t, s)
				missing := fixture.ImplicitError(`SELECT * FROM bad`, fixture.Diagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
				missing.Prepared = prepared
				p.Step(t, missing)
			})
		}
	}
}
