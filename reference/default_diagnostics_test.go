//go:build reference

package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceDefaultDiagnosticsAndConversionTiming(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			for _, tc := range []struct {
				q    string
				want referenceDiagnostic
			}{
				{`CREATE TABLE bad (i integer DEFAULT TRUE)`, referenceDiagnostic{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
				{`CREATE TABLE bad (i integer DEFAULT 'oops')`, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT '2147483648')`, referenceDiagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT i)`, referenceDiagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT $1)`, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT 1 DEFAULT 2)`, referenceDiagnostic{Code: "42601", Message: `multiple default values specified for column "i" of table "bad"`, Position: "39"}},
			} {
				s := referenceImplicitError(tc.q, tc.want)
				s.errorStage = "execute"
				run(s)
				run(referenceImplicitError(`SELECT * FROM bad`, referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
			}
			for _, tc := range []struct{ q, token, code, message string }{
				{`CREATE TABLE bad(i BOOL DEFAULT (TRUE OR i=1))`, "i=1", "0A000", "cannot use column reference in DEFAULT expression"},
				{`/*é*/ CREATE TABLE bad(i BOOL DEFAULT (FALSE AND $2))`, "$2", "42P02", "there is no parameter $2"},
			} {
				position := fmt.Sprint(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position})
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec(`CREATE TABLE narrowed(i integer DEFAULT 2147483648)`, 0))
			run(referenceImplicitError(`INSERT INTO narrowed DEFAULT VALUES`, referenceDiagnostic{Code: "22003", Message: "integer out of range"}))
			run(referenceSelect(`SELECT * FROM narrowed`, []string{"i"}, []string{"INT4"}, nil))
			run(referenceExec(`CREATE TABLE sized(id INT,v NUMERIC(3,1) DEFAULT 12.34)`, 0))
			run(referenceExec(`INSERT INTO sized(id) VALUES(1)`, 1))
			run(referenceSelect(`SELECT * FROM sized`, []string{"id", "v"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "12.3"}}))
			run(referenceExec(`CREATE TABLE overflow(id INT,v NUMERIC(3,1) DEFAULT 99.95)`, 0))
			run(referenceExec(`INSERT INTO overflow VALUES(1,1)`, 1))
			run(referenceImplicitError(`INSERT INTO overflow(id) VALUES(2),(3)`, referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}))
			run(referenceSelect(`SELECT * FROM overflow`, []string{"id", "v"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "1.0"}}))
			run(referenceExec(`CREATE TABLE nn(id INT,v INT DEFAULT NULL NOT NULL)`, 0))
			run(referenceExec(`INSERT INTO nn VALUES(1,9)`, 1))
			run(referenceImplicitError(`INSERT INTO nn VALUES(2,9),(3,DEFAULT)`, referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Schema: "public", Table: "nn", Column: "v", Detail: "Failing row contains (3, null)."}))
			run(referenceSelect(`SELECT * FROM nn`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
			run(referenceExec(`CREATE TABLE plain_nn(id INT,v INT NOT NULL)`, 0))
			run(referenceExec(`INSERT INTO plain_nn VALUES(1,9)`, 1))
			run(referenceImplicitError(`INSERT INTO plain_nn VALUES(2,9),(3,DEFAULT)`, referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "plain_nn" violates not-null constraint`, Schema: "public", Table: "plain_nn", Column: "v", Detail: "Failing row contains (3, null)."}))
			run(referenceSelect(`SELECT * FROM plain_nn`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
		})
	}
}

func TestReferenceDefaultExternalVolatileAndSubqueryBoundaries(t *testing.T) {
	p := newReferencePair(t)
	record := &referenceRecorder{}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, record, s) }
	// PostgreSQL-only adjacent SQL features, not native success/parity promises.
	run(referenceExec(`CREATE SEQUENCE default_seq`, 0))
	run(referenceExec(`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, 0))
	run(referenceExec(`INSERT INTO volatile_d(id) VALUES(1),(2),(3)`, 3))
	run(referenceExec(`INSERT INTO volatile_d VALUES(4,DEFAULT)`, 1))
	run(referenceSelect(`SELECT * FROM volatile_d ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}))
	run(referenceImplicitError(`CREATE TABLE bad (i integer DEFAULT (SELECT 1))`, referenceDiagnostic{Code: "0A000", Message: "cannot use subquery in DEFAULT expression", Position: "37"}))
	// Native rejects the excluded grammar rather than inventing a function result.
	for _, tc := range []struct{ q, code, message, token string }{
		{`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, "0A000", "functions are unsupported", "nextval"},
		{`CREATE TABLE bad (i integer DEFAULT (SELECT 1))`, "42601", `syntax error at or near "SELECT"`, "SELECT"},
	} {
		position := fmt.Sprint(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
		referenceRunStep(t, p.ctx, p.memory, &referenceRecorder{}, referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position}))
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
				p := newReferencePair(t)
				s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position})
				s.prepared, s.errorStage = prepared, "execute"
				p.step(t, s)
				missing := referenceImplicitError(`SELECT * FROM bad`, referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
				missing.prepared = prepared
				p.step(t, missing)
			})
		}
	}
}
