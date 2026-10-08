package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestLikeWrapperComparisonDemand(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")")
	execSQL(t, db, "INSERT INTO t VALUES('a','z')")
	for state := range 3 {
		if state == 1 {
			execSQL(t, db, "UPDATE t SET c=NULL")
		}
		if state == 2 {
			execSQL(t, db, "UPDATE t SET p=NULL")
		}
		for _, function := range []string{"NULLIF", "GREATEST", "LEAST"} {
			q := "SELECT 'a' LIKE " + function + "(c,p) FROM t"
			t.Run(q, func(t *testing.T) {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.Query()
					} else {
						rows, err = db.Query(q)
					}
					if state == 0 || state == 1 && function != "NULLIF" {
						if rows != nil {
							rows.Close()
							t.Fatal("conflict exposed row cursor")
						}
						message := "could not determine which collation to use for string comparison"
						if state == 1 {
							message = "could not determine which collation to use for LIKE"
						}
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: message, Hint: "Use the COLLATE clause to set the collation explicitly."})
					} else {
						compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{nil}})
					}
				}
			})
		}
	}
}

func TestLikeViewCollationPublication(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")")
	execSQL(t, db, "INSERT INTO t VALUES('a','a')")
	for _, expr := range []string{"COALESCE(pg_catalog.like_escape(c,p),'a')", "NULLIF(c,p)", "GREATEST(c,p)", "LEAST(c,p)"} {
		q := "CREATE VIEW escaped_pattern AS SELECT " + expr + " AS pattern FROM t"
		t.Run(q, func(t *testing.T) {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal("view output conflict rejected Prepare", err)
			}
			defer stmt.Close()
			_, err = db.Exec(q)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for view column \"pattern\"", Hint: "Use the COLLATE clause to set the collation explicitly."})
			_, err = stmt.Exec()
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for view column \"pattern\"", Hint: "Use the COLLATE clause to set the collation explicitly."})
		})
	}
	// The same name is available after every failed publication, and the
	// surviving determinate output retains POSIX rather than default identity.
	execSQL(t, db, "CREATE VIEW escaped_pattern AS SELECT CAST(p AS TEXT) AS pattern FROM t")
	rows, err := db.Query("SELECT 'a' LIKE pattern FROM escaped_pattern")
	compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{true}})
	_, err = db.Query("SELECT a.c LIKE b.pattern FROM t a JOIN escaped_pattern b ON TRUE")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
}
