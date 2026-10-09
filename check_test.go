package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func checkViolation(table, name, detail string) stuntgres.Error {
	return stuntgres.Error{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + name + `"`, Detail: "Failing row contains (" + detail + ").", Schema: "public", Table: table, Constraint: name}
}
func checkExecError(t *testing.T, db *sql.DB, prepared bool, q string, want stuntgres.Error, args ...any) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		s, e := db.Prepare(q)
		if e != nil {
			t.Fatal("Prepare", q, e)
		}
		defer s.Close()
		result, err = s.Exec(args...)
	} else {
		result, err = db.Exec(q, args...)
	}
	if result != nil {
		t.Fatal("failed statement exposed result", result)
	}
	notNullDiagnostic(t, err, want)
}
