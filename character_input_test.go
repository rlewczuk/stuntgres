package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestCharacterSQLZeroEscapeDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE inputs(t TEXT)")
	for _, tc := range []struct {
		query string
		want  stuntgres.Error
	}{
		{`INSERT INTO inputs(t) VALUES(E'\000')`, stuntgres.Error{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`}},
		{`INSERT INTO inputs(t) VALUES(U&'\0000')`, stuntgres.Error{Code: "42601", Message: "invalid Unicode escape value", Position: "33"}},
	} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				s, e := db.Prepare(tc.query)
				err = e
				if s != nil {
					s.Close()
				}
			} else {
				_, err = db.Exec(tc.query)
			}
			typeCoreDiagnostic(t, err, tc.want)
		}
	}
	wantTuples(t, db, "SELECT t FROM inputs", [][]any{})
}
