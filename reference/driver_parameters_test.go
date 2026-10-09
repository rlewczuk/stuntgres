package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDirectAndPreparedParameters(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					driverExecSQL(t, db, "CREATE TABLE p (a INT, b INT, text TEXT)")
					defer driverExecSQL(t, db, "DROP TABLE p")
					query := "INSERT INTO p (a,b) VALUES ($1,$1),($2,$2)"
					var r sql.Result
					var err error
					if prepared {
						stmt, e := db.Prepare(query)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						r, err = stmt.Exec(2, "3")
					} else {
						r, err = db.Exec(query, 2, "3")
					}
					if err != nil {
						t.Fatal(err)
					}
					n, err := r.RowsAffected()
					if err != nil || n != 2 {
						t.Fatalf("affected: %d %v", n, err)
					}
					driverWantTuples(t, db, "SELECT a,b,text FROM p ORDER BY a", [][]any{{int64(2), int64(2), nil}, {int64(3), int64(3), nil}})
					referenceRunStep(t, p.ctx, db, backend.recorder, referenceSelect("SELECT a,b,text FROM p ORDER BY a", []string{"a", "b", "text"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(2), nil}, {int64(3), int64(3), nil}}))
					result, err := db.Exec("INSERT INTO p(a,b) VALUES ($1,$1)")
					if result != nil {
						t.Fatal("missing arguments exposed result")
					}
					if backend.pg {
						// With no arguments pq uses the simple-query protocol:
						// PostgreSQL analyzes $1 without a supplied parameter frame.
						want := controlsReferenceDiagnostic("42P02", "there is no parameter $1")
						want.Position = "28"
						referenceAssertDiagnostic(t, err, &want)
					} else {
						driverArgumentError(t, err)
					}
					result, err = db.Exec("INSERT INTO p(a) VALUES ($1)", 1, 2)
					if result != nil {
						t.Fatal("extra arguments exposed result")
					}
					driverArgumentError(t, err)
					_, err = db.Exec("INSERT INTO p(a) VALUES ($1)", sql.Named("a", 1))
					if backend.pg {
						if err != nil {
							t.Fatal(err)
						}
						driverWantTuples(t, db, "SELECT a FROM p WHERE a=1", [][]any{{int64(1)}})
						driverExecSQL(t, db, "DELETE FROM p WHERE a=1")
					} else {
						driverArgumentError(t, err)
					}
					failed, err := db.Prepare("INSERT INTO p(a) VALUES ($2)")
					if failed != nil {
						failed.Close()
						t.Fatal("gap Prepare exposed a statement")
					}
					driverSQLState(t, err, "42P18")
					failed, err = db.Prepare("INSERT INTO p(a,text) VALUES ($1,$1)")
					if failed != nil {
						failed.Close()
						t.Fatal("mixed-slot Prepare exposed a statement")
					}
					if backend.pg {
						driverSQLState(t, err, "42P08")
					} else {
						driverSQLState(t, err, "0A000")
					}
				})
			}
		})
	}
}

func TestReferenceTypedValuesAndAtomicInsert(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			driverExecSQL(t, db, "CREATE TABLE typed (i INT NOT NULL, big BIGINT, text TEXT, flag BOOL)")
			for _, tc := range []struct {
				query, state string
				args         []any
			}{
				{"INSERT INTO typed(i) VALUES (2147483648)", "22003", nil},
				{"INSERT INTO typed(i) VALUES ('not an int')", "22P02", nil},
				{"INSERT INTO typed(i) VALUES (TRUE)", "42804", nil},
				{"INSERT INTO typed(i) VALUES (NULL)", "23502", nil},
				{"INSERT INTO typed(i) VALUES (1),(2147483648)", "22003", nil},
				{"INSERT INTO typed(i,flag) VALUES (1,'o')", "22P02", nil},
				{"INSERT INTO typed(i) VALUES ($1)", "22P02", []any{1.5}},
			} {
				_, err := db.Exec(tc.query, tc.args...)
				driverSQLState(t, err, tc.state)
				driverWantTuples(t, db, "SELECT i FROM typed", [][]any{})
			}
			driverExecSQL(t, db, "INSERT INTO typed VALUES (-2147483648,-9223372036854775808,TRUE,' YeS '),(2147483647,9223372036854775807,42,'of')")
			flag, text := []byte("on"), []byte("owned")
			driverExecSQL(t, db, "INSERT INTO typed(i,flag,text) VALUES ($1,$2,$3)", " +0 ", flag, text)
			flag[0], text[0] = 'X', 'X'
			driverWantTuples(t, db, "SELECT i,big,text,flag FROM typed ORDER BY i", [][]any{{int64(-2147483648), int64(-9223372036854775808), "true", true}, {int64(0), nil, "owned", true}, {int64(2147483647), int64(9223372036854775807), "42", false}})
			referenceRunStep(t, p.ctx, db, backend.recorder, referenceSelect("SELECT i,big,text,flag FROM typed ORDER BY i", []string{"i", "big", "text", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "true", true}, {int64(0), nil, "owned", true}, {int64(2147483647), int64(9223372036854775807), "42", false}}))
			_, err := db.Query("SELECT i FROM typed WHERE flag = 1")
			driverSQLState(t, err, "42883")
		})
	}
}

func TestReferenceReferencesNullsAndOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			driverExecSQL(t, db, "CREATE TABLE ord (v INT, label TEXT)")
			for _, q := range []string{"SELECT missing FROM ord", "SELECT v FROM ord WHERE missing = 1", "SELECT v FROM ord ORDER BY missing"} {
				_, err := db.Query(q)
				driverSQLState(t, err, "42703")
			}
			driverExecSQL(t, db, "INSERT INTO ord VALUES (NULL,'n'),(2,'b'),(1,'a')")
			driverWantTuples(t, db, "SELECT v FROM ord ORDER BY v", [][]any{{int64(1)}, {int64(2)}, {nil}})
			driverWantTuples(t, db, "SELECT v FROM ord ORDER BY v DESC", [][]any{{nil}, {int64(2)}, {int64(1)}})
			driverWantTuples(t, db, "SELECT v FROM ord WHERE v = NULL", [][]any{})
			driverWantTuples(t, db, "SELECT v FROM ord WHERE v = $1", [][]any{}, nil)
			_, err := db.Exec("CREATE TABLE ord (v INT)")
			driverSQLState(t, err, "42P07")
			_, err = db.Exec("CREATE TABLE duplicate (v INT,v TEXT)")
			driverSQLState(t, err, "42701")
			_, err = db.Exec("INSERT INTO ord(v,v) VALUES(1,2)")
			driverSQLState(t, err, "42701")
		})
	}
}
