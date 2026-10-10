package reference_test

import "testing"

func TestReferenceCollationSeedTemporaryLifecycle(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		owner := sequenceCoreConn(t, p.ctx, b.db)
		run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, owner, b.recorder, s) }
		exec := func(queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(referenceExec(q, 0))
			}
		}
		exec(`CREATE COLLATION pg_temp.coll_seed_temp_marker FROM pg_catalog."C"`, `CREATE COLLATION coll_seed_temp_durable FROM pg_catalog."C"`)
		// Repair the pin even if an implementation incorrectly admits the first move.
		defer owner.ExecContext(p.ctx, `ALTER COLLATION pg_temp."C" SET SCHEMA pg_catalog`)
		markerOID := collationOID(t, b.db, "coll_seed_temp_marker")
		durableOID := collationOID(t, b.db, "coll_seed_temp_durable")
		queries := []string{`ALTER COLLATION pg_catalog."C" SET SCHEMA pg_temp`, `ALTER COLLATION coll_seed_temp_durable SET SCHEMA pg_temp`, `ALTER COLLATION pg_temp.coll_seed_temp_marker SET SCHEMA public`}
		for _, prepared := range []bool{false, true} {
			for _, q := range queries {
				step := referenceErrorDiagnostic(q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot move objects into or out of temporary schemas"})
				step.prepared, step.errorStage = prepared, "execute"
				run(step)
			}
		}
		if collationOID(t, b.db, "coll_seed_temp_marker") != markerOID || collationOID(t, b.db, "coll_seed_temp_durable") != durableOID {
			t.Fatal("failed TEMP move changed identity")
		}
		run(referenceSelect(`SELECT collname,collprovider,collencoding,pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE collname='C' OR collname='coll_seed_temp_durable' OR collname='coll_seed_temp_marker' ORDER BY collname`, []string{"collname", "collprovider", "collencoding", "visible"}, []string{"NAME", "CHAR", "INT4", "BOOL"}, [][]any{{"C", "c", int64(-1), true}, {"coll_seed_temp_durable", "c", int64(-1), true}, {"coll_seed_temp_marker", "c", int64(-1), false}}))
		run(referenceSelect(`SELECT upper('é' COLLATE pg_catalog."C") AS c,upper('é' COLLATE coll_seed_temp_durable) AS durable,upper('é' COLLATE pg_temp.coll_seed_temp_marker) AS temporary`, []string{"c", "durable", "temporary"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"é", "é", "é"}}))
		exec(`DROP COLLATION coll_seed_temp_durable`, `DROP COLLATION pg_temp.coll_seed_temp_marker`)
	})
}
