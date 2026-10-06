package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

// This regression intentionally uses only existing public APIs, so the same
// behavioral assertions run before and after the defaults implementation.
func TestDefaultRegressionBeforeFix(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			exec := func(q string, count int64) { tableCoreExec(t, db, prepared, q, count) }
			exec(`CREATE TABLE d(id integer, i integer DEFAULT 7, s text DEFAULT 'O''Brien', b boolean DEFAULT TRUE, n integer)`, 0)
			tableCoreMetadata(t, db, prepared, `SELECT * FROM d`, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, []bool{true, true, true, true, true})
			exec(`INSERT INTO d(id) VALUES(1),(2)`, 2)
			exec(`INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT)`, 1)
			exec(`INSERT INTO d(id,i) VALUES(4,NULL)`, 1)
			exec(`INSERT INTO d VALUES(5)`, 1)
			wantTuples(t, db, `SELECT * FROM d ORDER BY id`, [][]any{
				{int64(1), int64(7), "O'Brien", true, nil},
				{int64(2), int64(7), "O'Brien", true, nil},
				{int64(3), int64(7), "O'Brien", true, nil},
				{int64(4), nil, "O'Brien", true, nil},
				{int64(5), int64(7), "O'Brien", true, nil},
			})
			exec(`INSERT INTO d DEFAULT VALUES`, 1)
			rows, err := db.Query(`SELECT * FROM d`)
			queryCoreRows(t, rows, err, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, [][]any{
				{int64(1), int64(7), "O'Brien", true, nil},
				{int64(2), int64(7), "O'Brien", true, nil},
				{int64(3), int64(7), "O'Brien", true, nil},
				{int64(4), nil, "O'Brien", true, nil},
				{int64(5), int64(7), "O'Brien", true, nil},
				{nil, int64(7), "O'Brien", true, nil},
			}, true)
			exec(`CREATE TABLE z()`, 0)
			exec(`INSERT INTO z DEFAULT VALUES`, 1)
			wantTuples(t, db, `SELECT * FROM z`, [][]any{{}})
			exec(`CREATE TABLE all_defaults(i integer DEFAULT 7, s text DEFAULT 'O''Brien', b boolean DEFAULT TRUE, n integer)`, 0)
			exec(`INSERT INTO all_defaults DEFAULT VALUES`, 1)
			wantTuples(t, db, `SELECT * FROM all_defaults`, [][]any{{int64(7), "O'Brien", true, nil}})
			const invalidTargets = `INSERT INTO all_defaults(i) DEFAULT VALUES`
			if prepared {
				stmt, prepareErr := db.Prepare(invalidTargets)
				if stmt != nil {
					stmt.Close()
				}
				err = prepareErr
			} else {
				_, err = db.Exec(invalidTargets)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `syntax error at or near "DEFAULT"`, Position: "29"})
			wantTuples(t, db, `SELECT * FROM all_defaults`, [][]any{{int64(7), "O'Brien", true, nil}})
			_, err = db.Exec(`INSERT INTO d(id,i) VALUES(6)`)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: "INSERT has more target columns than expressions", Position: "18"})
		})
	}
}
