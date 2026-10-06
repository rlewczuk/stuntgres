package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestCharacterAffectedCountsAndFailedStatement(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		query string
		count int64
	}{
		{"CREATE TABLE c(v VARCHAR(2))", 0},
		{"INSERT INTO c VALUES('ok'),('é🙂'),(NULL)", 3},
	} {
		result := execSQL(t, db, tc.query)
		got, err := result.RowsAffected()
		if err != nil || got != tc.count {
			t.Fatal(tc.query, got, err)
		}
	}
	for _, prepared := range []bool{false, true} {
		var err error
		if prepared {
			s, e := db.Prepare("INSERT INTO c VALUES('a'),('bad')")
			if e != nil {
				t.Fatal("valid INSERT must prepare before sizing", e)
			}
			result, e := s.Exec()
			s.Close()
			err = e
			if result != nil {
				if n, e := result.RowsAffected(); e == nil && n != 0 {
					t.Fatal("failed statement exposed successful affected count", n)
				}
			}
		} else {
			result, e := db.Exec("INSERT INTO c VALUES('a'),('bad')")
			err = e
			if result != nil {
				if n, e := result.RowsAffected(); e == nil && n != 0 {
					t.Fatal("failed statement exposed successful affected count", n)
				}
			}
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type character varying(2)"})
		rows, queryErr := db.Query("SELECT * FROM c")
		queryCoreRows(t, rows, queryErr, []string{"v"}, []string{"VARCHAR"}, [][]any{{"ok"}, {"é🙂"}, {nil}}, true)
	}
}
