package stuntgres_test

import (
	"database/sql"
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

func TestInsertDefaultMixedParameters(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, `CREATE TABLE d(id INT,v INT DEFAULT 7,s TEXT DEFAULT 'fallback',n INT)`, 0)
			names := []string{"id", "v", "s", "n"}
			types := []string{"INT4", "INT4", "TEXT", "INT4"}
			tableCoreMetadata(t, db, prepared, `SELECT * FROM d`, names, types, []bool{true, true, true, true})
			const insert = `INSERT INTO d(s,id,v) VALUES($1,$2,DEFAULT),(DEFAULT,$3,$4)`
			var stmt *sql.Stmt
			if prepared {
				var err error
				stmt, err = db.Prepare(insert)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			for _, args := range [][]any{{"given", 1, 2, 9}, {"again", 3, 4, nil}} {
				if stmt == nil {
					tableCoreExec(t, db, false, insert, 2, args...)
					continue
				}
				result, err := stmt.Exec(args...)
				if err != nil {
					t.Fatal(insert, err)
				}
				if count, err := result.RowsAffected(); err != nil || count != 2 {
					t.Fatal("RowsAffected", count, err)
				}
			}
			defaultConsumerRows(t, db, prepared, names, types, [][]any{
				{int64(1), int64(7), "given", nil},
				{int64(2), int64(9), "fallback", nil},
				{int64(3), int64(7), "again", nil},
				{int64(4), nil, "fallback", nil},
			})
		})
	}
}
