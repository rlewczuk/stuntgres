package stuntgres_test

import (
	"stuntgres"
	"testing"
)

// This independently specified public regression must fail before numeric support.
func TestNumericExactPublicRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "numeric-exact-regression")
	execSQL(t, db, "CREATE TABLE n(v NUMERIC)")
	execSQL(t, db, "INSERT INTO n VALUES ('12345678901234567890.1234567890123456789000')")
	wantTuples(t, db, "SELECT v FROM n", [][]any{{"12345678901234567890.1234567890123456789000"}})
}
