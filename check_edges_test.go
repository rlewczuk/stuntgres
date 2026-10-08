package stuntgres_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestCheckDiagnosticOutputAndQuotedIdentity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE "t""é\\x"(a INT,s TEXT,f BOOL,CONSTRAINT "c""é\\x" CHECK(a=1))`)
	text := strings.Repeat("界", 30)
	checkExecError(t, db, true, `INSERT INTO "t""é\\x" VALUES($1,$2,$3)`, checkViolation(`t"é\\x`, `c"é\\x`, "2, "+strings.Repeat("界", 21)+"..., f"), 2, text, false)
}
func TestCheckGeneratedUTF8Names(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	table := strings.Repeat("界", 20)
	column := strings.Repeat("文", 20)
	// PG makeObjectName balances byte lengths before rune-safe clipping:
	// 63 - len("_"+"_check") = 56; both fields clip to 27 bytes.
	name := strings.Repeat("界", 9) + "_" + strings.Repeat("文", 9) + "_check"
	execSQL(t, db, `CREATE TABLE "`+table+`"("`+column+`" INT CHECK("`+column+`"=1))`)
	checkExecError(t, db, false, `INSERT INTO "`+table+`" VALUES(2)`, checkViolation(table, name, "2"))
}
func TestCheckUnknownAndUnicodeDefinitionPositions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	checkExecError(t, db, false, "/*é*/ CREATE TABLE t(a INT CHECK(a))", stuntgres.Error{Code: "42804", Message: "argument of CHECK must be type boolean, not type integer", Position: "34"})
	for _, column := range []string{"xmin", "cmin", "xmax", "cmax"} {
		checkExecError(t, db, false, "CREATE TABLE t(a INT CHECK("+column+" IS NULL))", stuntgres.Error{Code: "42P10", Message: `system column "` + column + `" reference in check constraint is invalid`, Position: "28"})
	}
	checkExecError(t, db, false, "CREATE TABLE t(a INT CHECK('not_bool'))", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "not_bool"`, Position: "28"})
}
