package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestCharacterUnicodeAssignmentBoundaries(t *testing.T) {
	for _, decl := range []string{"varchar(2)", "char(2)"} {
		t.Run(decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE c(v "+decl+")")
			execSQL(t, db, "INSERT INTO c VALUES('é🙂'),('é')")
			dbType := "VARCHAR"
			if decl == "char(2)" {
				dbType = "BPCHAR"
			}
			rows, err := db.Query("SELECT v FROM c")
			queryCoreRows(t, rows, err, []string{"v"}, []string{dbType}, [][]any{{"é🙂"}, {"e\u0301"}}, true)
			name := "character varying(2)"
			if decl == "char(2)" {
				name = "character(2)"
			}
			for _, value := range []string{"é🙂x", "e\u0301x", "a🙂é"} {
				for _, prepared := range []bool{false, true} {
					var err error
					if prepared {
						s, e := db.Prepare("INSERT INTO c VALUES($1)")
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.Exec([]byte(value))
						s.Close()
					} else {
						_, err = db.Exec("INSERT INTO c VALUES($1)", value)
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type " + name})
				}
			}
			execSQL(t, db, "INSERT INTO c VALUES($1)", "é🙂   ")
			rows, err = db.Query("SELECT v FROM c")
			queryCoreRows(t, rows, err, []string{"v"}, []string{dbType}, [][]any{{"é🙂"}, {"e\u0301"}, {"é🙂"}}, true)
		})
	}
}
