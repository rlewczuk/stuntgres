package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationTransactionDependencies(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{`CREATE COLLATION coll_tx_a FROM pg_catalog."C"`, `CREATE COLLATION coll_tx_b FROM pg_catalog."C"`, `CREATE TABLE coll_tx_data(id integer,v text COLLATE coll_tx_b)`} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO coll_tx_data VALUES(7,'é')", 1))
		a, bid := fixture.CollationOID(t, b.DB, "coll_tx_a"), fixture.CollationOID(t, b.DB, "coll_tx_b")
		edge := fixture.Select("SELECT d.refobjid::bigint AS refobjid,d.deptype FROM pg_depend d JOIN pg_class c ON d.objid=c.oid WHERE d.classid=1259 AND c.relname='coll_tx_data' AND d.refclassid=3456", []string{"refobjid", "deptype"}, []string{"INT8", "CHAR"}, [][]any{{bid, "n"}})
		run(edge)
		for _, commit := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT healthy", 0))
			diagnostic := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop desired object(s) because other objects depend on them")
			diagnostic.Detail = "column v of table coll_tx_data depends on collation coll_tx_b"
			diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic("DROP COLLATION coll_tx_a,coll_tx_b", diagnostic))
			run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			run(fixture.Exec("ROLLBACK TO healthy", 0))
			for _, oid := range []int64{a, bid} {
				run(fixture.Select("SELECT oid::bigint FROM pg_collation WHERE oid=$1", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}, oid))
			}
			run(edge)
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id,v FROM coll_tx_data", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "é"}}))
			run(fixture.Exec("DROP COLLATION coll_tx_b CASCADE", 0))
			run(fixture.Select("SELECT * FROM coll_tx_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			// Heap reads would wait on our own uncommitted ALTER TABLE. The catalog
			// snapshot still exposes the peer's committed column and dependency.
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT a.attname FROM pg_attribute a JOIN pg_class c ON a.attrelid=c.oid WHERE c.relname='coll_tx_data' AND a.attname='v' AND NOT a.attisdropped", []string{"attname"}, []string{"NAME"}, [][]any{{"v"}}))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, edge)
			run(fixture.Exec("ROLLBACK TO healthy", 0))
			run(edge)
			run(fixture.Select("SELECT id,v FROM coll_tx_data", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "é"}}))
			run(fixture.Exec("DROP COLLATION coll_tx_b CASCADE", 0))
			run(fixture.Exec("RELEASE healthy", 0))
			end := "ROLLBACK"
			if commit {
				end = "COMMIT"
			}
			run(fixture.Exec(end, 0))
			if !commit {
				run(edge)
			} else {
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT * FROM coll_tx_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE SCHEMA coll_private", 0))
		for _, q := range []string{`CREATE COLLATION coll_private.source FROM pg_catalog.pg_unicode_fast`, `CREATE COLLATION coll_private.copy FROM coll_private.source`, `DROP COLLATION coll_private.source`} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT upper('ß' COLLATE coll_private.copy) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"SS"}}))
		run(fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=3456 AND refclassid=3456", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='coll_private'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
