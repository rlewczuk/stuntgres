package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestFloatPrecisionScannerDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "float-precision-scanner")
	for _, spelling := range []string{"1.5junk", ".5junk", "1.5e+", ".5e-", "1.5e", "1.5e+3junk", "1.5_", "1.5e+3_"} {
		q := "CREATE TABLE f(x FLOAT(" + spelling + "))"
		want := stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "` + spelling + `"`, Position: "24"}
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("malformed precision prepared")
		}
		typeCoreDiagnostic(t, err, want)
	}
	q := "CREATE TABLE f(x FLOAT(1..5))"
	want := stuntgres.Error{Code: "42601", Message: `syntax error at or near ".."`, Position: "25"}
	_, err := db.Exec(q)
	typeCoreDiagnostic(t, err, want)
	stmt, err := db.Prepare(q)
	if stmt != nil {
		stmt.Close()
		t.Fatal("doubled-dot precision prepared")
	}
	typeCoreDiagnostic(t, err, want)
	// Failed declarations publish no relation; valid creation must still succeed.
	execSQL(t, db, "CREATE TABLE f(r REAL)")
	wantTuples(t, db, "SELECT * FROM f", [][]any{})
}
