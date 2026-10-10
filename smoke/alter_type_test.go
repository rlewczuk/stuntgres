package smoke_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterTypeCheckFloatingSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			var notices []stuntgres.Error
			db := alterDropSmokePool(t, &notices)
			alterDropSmokeExec(t, db, "CREATE TABLE t(c REAL CONSTRAINT ck CHECK(c>'1.0000001' AND c<>NULL))")
			alterDropSmokeExec(t, db, "INSERT INTO t VALUES(2),(NULL)")
			action := "ALTER TABLE t ALTER c TYPE DOUBLE PRECISION USING c"
			if prepared {
				stmt, err := db.Prepare(action)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := stmt.Exec(); err != nil {
					stmt.Close()
					t.Fatal(err)
				}
				stmt.Close()
			} else {
				alterDropSmokeExec(t, db, action)
			}
			reject := func() {
				_, err := db.Exec("INSERT INTO t VALUES(1.00000011)")
				want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (1.00000011).", Schema: "public", Table: "t", Constraint: "ck"}
				var diagnostic *stuntgres.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal(diagnostic, err, want)
				}
			}
			accept := func() {
				var result sql.Result
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare("INSERT INTO t VALUES(1.00000013)")
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					defer stmt.Close()
					result, err = stmt.Exec()
				} else {
					result, err = db.Exec("INSERT INTO t VALUES(1.00000013)")
				}
				if err != nil {
					t.Fatal(err)
				}
				if count, err := result.RowsAffected(); err != nil || count != 1 {
					t.Fatal(count, err)
				}
			}
			reject()
			rows, err := db.Query("SELECT c FROM t ORDER BY c")
			if err != nil {
				t.Fatal(err)
			}
			alterDropSmokeRows(t, rows, []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(2)}, {nil}})
			accept()
			rows, err = db.Query("SELECT c FROM t ORDER BY c")
			if err != nil {
				t.Fatal(err)
			}
			alterDropSmokeRows(t, rows, []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}})
			var dump bytes.Buffer
			if err := stuntgres.Dump(t.Context(), db, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "$1") || strings.Contains(dump.String(), "ALTER TABLE") {
				t.Fatal("private TYPE history leaked", dump.String())
			}
			if err := stuntgres.Reset(t.Context(), db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(t.Context(), db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			reject()
			rows, err = db.Query("SELECT c FROM t ORDER BY c")
			if err != nil {
				t.Fatal(err)
			}
			alterDropSmokeRows(t, rows, []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}})
			accept()
			rows, err = db.Query("SELECT c FROM t ORDER BY c")
			if err != nil {
				t.Fatal(err)
			}
			alterDropSmokeRows(t, rows, []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {nil}})
			if len(notices) != 0 {
				t.Fatal(notices)
			}
		})
	}
}
