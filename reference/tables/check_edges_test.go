package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckOutputAndUTF8Names(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE "t""é\\x"(a INT,s TEXT,f BOOL,CONSTRAINT "c""é\\x" CHECK(a=1))`, 0))
			run(fixture.Prepared(fixture.ImplicitError(`INSERT INTO "t""é\\x" VALUES($1,$2,$3)`, fixture.CheckViolation(`t"é\\x`, `c"é\\x`, "2, "+strings.Repeat("界", 21)+"..., f"), 2, strings.Repeat("界", 30), false)))
			table := strings.Repeat("界", 20)
			column := strings.Repeat("文", 20)
			name := strings.Repeat("界", 9) + "_" + strings.Repeat("文", 9) + "_check"
			run(fixture.Exec(`CREATE TABLE "`+table+`"("`+column+`" INT CHECK("`+column+`"=1))`, 0))
			run(fixture.ImplicitError(`INSERT INTO "`+table+`" VALUES(2)`, fixture.CheckViolation(table, name, "2")))
		})
	}
}
func TestReferenceCheckUnicodePosition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.ImplicitError("/*é*/ CREATE TABLE t(a INT CHECK(a))", fixture.Diagnostic{Code: "42804", Message: "argument of CHECK must be type boolean, not type integer", Position: "34"}))
			for _, column := range []string{"xmin", "cmin", "xmax", "cmax"} {
				run(fixture.ImplicitError("CREATE TABLE t(a INT CHECK("+column+" IS NULL))", fixture.Diagnostic{Code: "42P10", Message: `system column "` + column + `" reference in check constraint is invalid`, Position: "28"}))
			}
			run(fixture.ImplicitError("CREATE TABLE t(a INT CHECK('not_bool'))", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "not_bool"`, Position: "28"}))
		})
	}
}
