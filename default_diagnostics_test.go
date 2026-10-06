package stuntgres_test

import (
	"fmt"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestDefaultDiagnosticsAndConversionTiming(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			fail := func(q string, want stuntgres.Error) {
				t.Helper()
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("utility Prepare must succeed", q, e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, want)
			}
			for _, tc := range []struct {
				q    string
				want stuntgres.Error
			}{
				{`CREATE TABLE bad (i integer DEFAULT TRUE)`, stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
				{`CREATE TABLE bad (i integer DEFAULT 'oops')`, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT '2147483648')`, stuntgres.Error{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT i)`, stuntgres.Error{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT $1)`, stuntgres.Error{Code: "42P02", Message: "there is no parameter $1", Position: "37"}},
				{`CREATE TABLE bad (i integer DEFAULT 1 DEFAULT 2)`, stuntgres.Error{Code: "42601", Message: `multiple default values specified for column "i" of table "bad"`, Position: "39"}},
			} {
				fail(tc.q, tc.want)
				_, err := db.Query(`SELECT * FROM bad`)
				sqlState(t, err, "42P01")
			}
			for _, tc := range []struct{ q, token, code, message string }{
				{`CREATE TABLE bad(i BOOL DEFAULT (TRUE OR i=1))`, "i=1", "0A000", "cannot use column reference in DEFAULT expression"},
				{`/*é*/ CREATE TABLE bad(i BOOL DEFAULT (FALSE AND $2))`, "$2", "42P02", "there is no parameter $2"},
			} {
				position := fmt.Sprint(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
				fail(tc.q, stuntgres.Error{Code: tc.code, Message: tc.message, Position: position})
			}
			tableCoreExec(t, db, prepared, `CREATE TABLE narrowed(i integer DEFAULT 2147483648)`, 0)
			fail(`INSERT INTO narrowed DEFAULT VALUES`, stuntgres.Error{Code: "22003", Message: "integer out of range"})
			wantTuples(t, db, `SELECT * FROM narrowed`, [][]any{})
			tableCoreExec(t, db, prepared, `CREATE TABLE sized(id INT,v NUMERIC(3,1) DEFAULT 12.34)`, 0)
			tableCoreExec(t, db, prepared, `INSERT INTO sized(id) VALUES(1)`, 1)
			wantTuples(t, db, `SELECT * FROM sized`, [][]any{{int64(1), "12.3"}})
			tableCoreExec(t, db, prepared, `CREATE TABLE overflow(id INT,v NUMERIC(3,1) DEFAULT 99.95)`, 0)
			tableCoreExec(t, db, prepared, `INSERT INTO overflow VALUES(1,1)`, 1)
			fail(`INSERT INTO overflow(id) VALUES(2),(3)`, stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."})
			wantTuples(t, db, `SELECT * FROM overflow`, [][]any{{int64(1), "1.0"}})
			tableCoreExec(t, db, prepared, `CREATE TABLE nn(id INT,v INT DEFAULT NULL NOT NULL)`, 0)
			tableCoreExec(t, db, prepared, `INSERT INTO nn VALUES(1,9)`, 1)
			fail(`INSERT INTO nn VALUES(2,9),(3,DEFAULT)`, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Schema: "public", Table: "nn", Column: "v", Detail: "Failing row contains (3, null)."})
			wantTuples(t, db, `SELECT * FROM nn`, [][]any{{int64(1), int64(9)}})
		})
	}
}

func TestDefaultDiagnosticPrecedence(t *testing.T) {
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
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				var err error
				if prepared {
					s, e := db.Prepare(tc.query)
					if e != nil {
						t.Fatal("utility Prepare must succeed", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(tc.query)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position})
				_, err = db.Query(`SELECT * FROM bad`)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
			})
		}
	}
}
