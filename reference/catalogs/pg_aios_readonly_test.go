package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGAIOSReadOnly(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		pgSequenceExec(t, p, b, b.DB, "CREATE TABLE aios_sentinel(n integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO aios_sentinel VALUES(42)",1))
		for _, tc := range []struct{query,verb,message string}{
			{"INSERT INTO pg_catalog.pg_aios DEFAULT VALUES","INSERT",`cannot insert into view "pg_aios"`},
			{"UPDATE pg_catalog.pg_aios SET pid=0","UPDATE",`cannot update view "pg_aios"`},
			{"DELETE FROM pg_catalog.pg_aios","DELETE",`cannot delete from view "pg_aios"`},
			{"TRUNCATE pg_catalog.pg_aios","TRUNCATE",`"pg_aios" is not a table`},
		} {
			want := fixture.Diagnostic{Code:"55000",Message:tc.message,Detail:"Views that do not select from a single table or view are not automatically updatable.",Hint:"To enable "+map[string]string{"INSERT":"inserting into","UPDATE":"updating","DELETE":"deleting from"}[tc.verb]+" the view, provide an INSTEAD OF "+tc.verb+" trigger or an unconditional ON "+tc.verb+" DO INSTEAD rule."}
			if tc.verb == "TRUNCATE" { want = fixture.Diagnostic{Code:"42809", Message:tc.message} }
			for _, prepared := range []bool{false,true} {
				s := fixture.ImplicitError(tc.query,want)
				s.Prepared, s.ErrorStage = prepared,"execute"
				if prepared && tc.verb != "TRUNCATE" { s.ErrorStage = "prepare" }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				aiosRead(t, p, b, b.DB, false)
				aiosSentinel(t, p, b, b.DB)
				shmemDefaultACL(t, p, b.DB, "pg_aios", b.PG)
			}
			if strings.Contains(tc.query,"TRUNCATE") { aiosCount(t, p, b, b.DB) }
		}
	}) }
}
