package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestDefaultSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			ctx := context.Background()
			exec := func(q string, n int64) {
				t.Helper()
				var result sql.Result
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					result, err = s.Exec()
					s.Close()
				} else {
					result, err = db.Exec(q)
				}
				if err != nil {
					t.Fatal(q, err)
				}
				if count, e := result.RowsAffected(); e != nil || count != n {
					t.Fatal(q, count, e)
				}
			}
			query := func(q string) *sql.Rows {
				t.Helper()
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					defer s.Close()
					rows, e := s.Query()
					if e != nil {
						t.Fatal(e)
					}
					return rows
				}
				rows, e := db.Query(q)
				if e != nil {
					t.Fatal(e)
				}
				return rows
			}
			check := func(want [][]any) {
				t.Helper()
				rows := query(`SELECT * FROM d ORDER BY id`)
				types, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				for i, c := range types {
					nullable, known := c.Nullable()
					scan := reflect.TypeFor[int64]()
					if i == 2 {
						scan = reflect.TypeFor[string]()
					}
					if i == 3 {
						scan = reflect.TypeFor[bool]()
					}
					if !known || !nullable || c.ScanType() != scan {
						t.Fatal("default changed declared metadata", i, c, nullable, known)
					}
				}
				alterDropSmokeRows(t, rows, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, want)
			}
			exec(`CREATE TABLE d(id INT,i INT DEFAULT 7,s TEXT DEFAULT 'O''Brien',b BOOL DEFAULT TRUE,n INT)`, 0)
			exec(`INSERT INTO d(id) VALUES(1),(2)`, 2)
			exec(`INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT)`, 1)
			exec(`INSERT INTO d(id,i) VALUES(4,NULL)`, 1)
			exec(`INSERT INTO d VALUES(5)`, 1)
			want := [][]any{{int64(1), int64(7), "O'Brien", true, nil}, {int64(2), int64(7), "O'Brien", true, nil}, {int64(3), int64(7), "O'Brien", true, nil}, {int64(4), nil, "O'Brien", true, nil}, {int64(5), int64(7), "O'Brien", true, nil}}
			check(want)
			exec(`CREATE TABLE logical_defaults(b BOOL DEFAULT (1 < 2 AND NOT FALSE),u BOOL DEFAULT (NOT NULL))`, 0)
			exec(`INSERT INTO logical_defaults DEFAULT VALUES`, 1)
			alterDropSmokeRows(t, query(`SELECT * FROM logical_defaults`), []string{"b", "u"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}})
			exec(`CREATE TABLE all_d(i INT DEFAULT 7,n INT)`, 0)
			exec(`INSERT INTO all_d DEFAULT VALUES`, 1)
			alterDropSmokeRows(t, query(`SELECT * FROM all_d`), []string{"i", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}})
			exec(`CREATE TABLE z()`, 0)
			exec(`INSERT INTO z DEFAULT VALUES`, 1)
			alterDropSmokeRows(t, query(`SELECT * FROM z`), []string{}, []string{}, [][]any{{}})
			for _, tc := range []struct{ query, message, position string }{
				{`INSERT INTO all_d(i) DEFAULT VALUES`, `syntax error at or near "DEFAULT"`, "22"},
				{`CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT NOT NULL)`, `syntax error at or near "NOT"`, "125"},
				{`CREATE TABLE bad(b BOOL DEFAULT TRUE AND FALSE)`, `syntax error at or near "AND"`, "38"},
				{`CREATE TABLE bad(b BOOL DEFAULT TRUE OR FALSE)`, `syntax error at or near "OR"`, "38"},
			} {
				if prepared {
					s, e := db.Prepare(tc.query)
					err = e
					if s != nil {
						s.Close()
					}
				} else {
					_, err = db.Exec(tc.query)
				}
				if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "42601", Message: tc.message, Position: tc.position}); e != nil {
					t.Fatal(tc.query, e)
				}
			}
			check(want)
			alterDropSmokeRows(t, query(`SELECT * FROM all_d`), []string{"i", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}})
			exec(`CREATE TABLE nn(id INT,v INT NOT NULL)`, 0)
			exec(`INSERT INTO nn VALUES(1,9)`, 1)
			_, err = db.Exec(`INSERT INTO nn VALUES(2,9),(3,DEFAULT)`)
			if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "nn", Column: "v"}); e != nil {
				t.Fatal(e)
			}
			alterDropSmokeRows(t, query(`SELECT * FROM nn`), []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}})
			stmt, err := db.Prepare(`CREATE TABLE bad (i integer DEFAULT TRUE)`)
			if err != nil {
				t.Fatal("utility preparation", err)
			}
			_, err = stmt.Exec()
			stmt.Close()
			if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}); e != nil {
				t.Fatal(e)
			}
			exec(`CREATE TABLE endpoint(id INT,v TIMESTAMP(6) DEFAULT TIMESTAMP(0) '294276-12-31 23:59:59.999999',b BOOL DEFAULT (TIMESTAMP(0) '294276-12-31 23:59:59.999999' = TIMESTAMP(0) '294276-12-31 23:59:59.999999'))`, 0)
			endpoint := time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)
			checkEndpoint := func(want [][]any) {
				t.Helper()
				rows := query(`SELECT * FROM endpoint ORDER BY id`)
				metadata, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				if metadata[1].ScanType() != reflect.TypeFor[time.Time]() {
					t.Fatal("endpoint ScanType", metadata[1])
				}
				if e := identifierCheckRows(rows, nil, []string{"id", "v", "b"}, []string{"INT4", "TIMESTAMP", "BOOL"}, want); e != nil {
					t.Fatal(e)
				}
			}
			exec(`INSERT INTO endpoint(id) VALUES(1)`, 1)
			checkEndpoint([][]any{{int64(1), endpoint, true}})
			var dump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(want)
			checkEndpoint([][]any{{int64(1), endpoint, true}})
			exec(`INSERT INTO endpoint VALUES(2,DEFAULT,DEFAULT)`, 1)
			checkEndpoint([][]any{{int64(1), endpoint, true}, {int64(2), endpoint, true}})
			exec(`INSERT INTO d(id) VALUES(6)`, 1)
			want = append(want, []any{int64(6), int64(7), "O'Brien", true, nil})
			check(want)
		})
	}
}
