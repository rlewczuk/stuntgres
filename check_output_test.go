package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestCheckPreparedExecutionOutputEnvironment(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE SCHEMA checks CREATE TABLE t(d DATE,b BYTEA,n INT CONSTRAINT c CHECK(n=1))")
	statement, err := db.Prepare("INSERT INTO checks.t VALUES($1,$2,$3)")
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	execSQL(t, db, "SET DateStyle TO 'SQL, DMY'")
	execSQL(t, db, "SET bytea_output TO 'escape'")
	result, err := statement.Exec("2024-01-02", []byte{0, 1, '\\'}, 2)
	if result != nil {
		t.Fatal("constraint failure exposed result")
	}
	want := checkViolation("t", "c", `02/01/2024, \000\001\\, 2`)
	want.Schema = "checks"
	notNullDiagnostic(t, err, want)
	execSQL(t, db, "SET DateStyle TO 'ISO, MDY'")
	execSQL(t, db, "SET bytea_output TO 'hex'")
	result, err = statement.Exec("2024-01-02", []byte{0, 1, '\\'}, 2)
	if result != nil {
		t.Fatal("constraint failure exposed result")
	}
	want = checkViolation("t", "c", `2024-01-02, \x00015c, 2`)
	want.Schema = "checks"
	notNullDiagnostic(t, err, want)
}
