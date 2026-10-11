package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationSeedTemporaryLifecycle(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
		exec := func(queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(fixture.Exec(q, 0))
			}
		}
		exec(`CREATE COLLATION pg_temp.coll_seed_temp_marker FROM pg_catalog."C"`, `CREATE COLLATION coll_seed_temp_durable FROM pg_catalog."C"`)
		// Repair the pin even if an implementation incorrectly admits the first move.
		defer owner.ExecContext(p.Ctx, `ALTER COLLATION pg_temp."C" SET SCHEMA pg_catalog`)
		markerOID := fixture.CollationOID(t, b.DB, "coll_seed_temp_marker")
		durableOID := fixture.CollationOID(t, b.DB, "coll_seed_temp_durable")
		queries := []string{`ALTER COLLATION pg_catalog."C" SET SCHEMA pg_temp`, `ALTER COLLATION coll_seed_temp_durable SET SCHEMA pg_temp`, `ALTER COLLATION pg_temp.coll_seed_temp_marker SET SCHEMA public`}
		for _, prepared := range []bool{false, true} {
			for _, q := range queries {
				step := fixture.ErrorDiagnostic(q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot move objects into or out of temporary schemas"})
				step.Prepared, step.ErrorStage = prepared, "execute"
				run(step)
			}
		}
		if fixture.CollationOID(t, b.DB, "coll_seed_temp_marker") != markerOID || fixture.CollationOID(t, b.DB, "coll_seed_temp_durable") != durableOID {
			t.Fatal("failed TEMP move changed identity")
		}
		run(fixture.Select(`SELECT collname,collprovider,collencoding,pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE collname='C' OR collname='coll_seed_temp_durable' OR collname='coll_seed_temp_marker' ORDER BY collname`, []string{"collname", "collprovider", "collencoding", "visible"}, []string{"NAME", "CHAR", "INT4", "BOOL"}, [][]any{{"C", "c", int64(-1), true}, {"coll_seed_temp_durable", "c", int64(-1), true}, {"coll_seed_temp_marker", "c", int64(-1), false}}))
		run(fixture.Select(`SELECT upper('é' COLLATE pg_catalog."C") AS c,upper('é' COLLATE coll_seed_temp_durable) AS durable,upper('é' COLLATE pg_temp.coll_seed_temp_marker) AS temporary`, []string{"c", "durable", "temporary"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"é", "é", "é"}}))
		exec(`DROP COLLATION coll_seed_temp_durable`, `DROP COLLATION pg_temp.coll_seed_temp_marker`)
	})
}
