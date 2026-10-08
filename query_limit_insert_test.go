package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryLimitInsertAssignment(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := queryLimitFixture(t)
			execSQL(t, db, "CREATE TABLE dst(id SMALLINT)")
			execute := func(q string, want int64) {
				t.Helper()
				var err error
				var count int64
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					r, e := stmt.Exec()
					err = e
					if err == nil {
						count, err = r.RowsAffected()
					}
				} else {
					r, e := db.Exec(q)
					err = e
					if err == nil {
						count, err = r.RowsAffected()
					}
				}
				if err != nil || count != want {
					t.Fatal(q, count, want, err)
				}
			}
			execute("INSERT INTO dst VALUES(1),(32768) LIMIT 1", 1)
			execute("INSERT INTO dst VALUES(32768) LIMIT 0", 0)
			execute("INSERT INTO dst VALUES(2),(32768),(3) OFFSET 2 LIMIT 1", 1)
			q := "INSERT INTO dst VALUES(2),(32768) LIMIT 2"
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = db.Exec(q)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
			queryLimitSelect(t, db, "SELECT id FROM dst ORDER BY id", "id", "INT2", queryLimitIDs(1, 3))
			execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
			execSQL(t, db, "CREATE TABLE defaults(id INT,n BIGINT DEFAULT 7)")
			execute("INSERT INTO defaults(id) SELECT nextval('s') FROM t OFFSET 1 LIMIT 1", 1)
			execute("INSERT INTO defaults(id) SELECT nextval('s') FROM t LIMIT 0", 0)
			queryLimitSelect(t, db, "SELECT n FROM defaults", "n", "INT8", queryLimitIDs(7))
			queryLimitSelect(t, db, "SELECT id FROM defaults", "id", "INT4", queryLimitIDs(2))
			rows, err := db.Query("SELECT nextval('s')")
			queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, queryLimitIDs(3), false)
			execSQL(t, db, "CREATE TABLE failing_default(id INT,n INT DEFAULT 1/0)")
			// Static destination simplification is admission, not a producing
			// default invocation: even zero retains this existing 22012.
			// The internal typed probe separately proves runtime default demand.
			for _, suffix := range []string{"LIMIT 0", "LIMIT 1"} {
				q = "INSERT INTO failing_default(id) SELECT id FROM t " + suffix
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
			}
			queryLimitSelect(t, db, "SELECT id FROM failing_default", "id", "INT4", queryLimitIDs())
			execute("INSERT INTO t SELECT id,v FROM t ORDER BY id LIMIT 1", 1)
			queryLimitSelect(t, db, "SELECT id FROM t ORDER BY id", "id", "INT4", queryLimitIDs(1, 1, 2, 3, 4))
		})
	}
}
