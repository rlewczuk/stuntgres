//go:build reference

package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceCheckOutputAndUTF8Names(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE "t""é\\x"(a INT,s TEXT,f BOOL,CONSTRAINT "c""é\\x" CHECK(a=1))`, 0))
		run(referencePrepared(referenceImplicitError(`INSERT INTO "t""é\\x" VALUES($1,$2,$3)`, referenceCheckViolation(`t"é\\x`, `c"é\\x`, "2, "+strings.Repeat("界", 21)+"..., f"), 2, strings.Repeat("界", 30), false)))
		table := strings.Repeat("界", 20)
		column := strings.Repeat("文", 20)
		name := strings.Repeat("界", 9) + "_" + strings.Repeat("文", 9) + "_check"
		run(referenceExec(`CREATE TABLE "`+table+`"("`+column+`" INT CHECK("`+column+`"=1))`, 0))
		run(referenceImplicitError(`INSERT INTO "`+table+`" VALUES(2)`, referenceCheckViolation(table, name, "2")))
	})
}
func TestReferenceCheckUnicodePosition(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceImplicitError("/*é*/ CREATE TABLE t(a INT CHECK(a))", referenceDiagnostic{Code: "42804", Message: "argument of CHECK must be type boolean, not type integer", Position: "34"}))
	})
}
