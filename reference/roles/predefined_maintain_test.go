package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func predefinedMaintain(s *membershipScenario, db *sql.DB) {
	s.t.Helper()
	s.exec("CREATE TABLE t(n INT)")
	s.run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
	s.exec("CREATE INDEX ix ON t(n)")
	initialTuples := float64(-1)
	if s.backend.PG {
		// PostgreSQL's CREATE INDEX heap scan has already published the
		// population; native index creation does not collect maintenance facts.
		initialTuples = 1
	}
	predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{initialTuples}})
	s.actor("pdef_actor")
	predefinedDenied(s, "REINDEX INDEX ix", "permission denied for index ix", false)
	predefinedDenied(s, "REINDEX TABLE t", "permission denied for table t", false)
	s.clearNotices()
	s.exec("ANALYZE t")
	s.notice("WARNING", "01000", `permission denied to analyze "t", skipping it`)
	s.clearNotices()
	predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{initialTuples}})
	s.actor("")
	s.exec("GRANT pg_maintain TO pdef_actor")
	s.actor("pdef_actor")
	s.exec("REINDEX INDEX ix")
	s.exec("REINDEX TABLE t")
	s.clearNotices()
	s.exec("ANALYZE t")
	if len(*s.notices) != 0 {
		s.t.Fatalf("pg_maintain ANALYZE notices %#v", *s.notices)
	}
	predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(1)}})
	predefinedDenied(s, "SELECT n FROM t", "permission denied for table t", true)
	predefinedDenied(s, "DROP INDEX ix", "must be owner of index ix", false)
	predefinedDenied(s, "ALTER INDEX ix RENAME TO denied", "must be owner of index ix", false)
	s.fail("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks", "")
	peer, err := db.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer peer.Close()
	defer peer.ExecContext(s.p.Ctx, "ROLLBACK")
	defer s.c.ExecContext(s.p.Ctx, "ROLLBACK")
	s.exec("BEGIN")
	s.exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Exec("BEGIN", 0))
	step := fixture.RoleReferenceError("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`, "", s.prepared, "execute")
	fixture.RunStep(s.t, s.p.Ctx, peer, s.record, step)
	fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Exec("ROLLBACK", 0))
	s.exec("ROLLBACK")
	fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Exec("BEGIN", 0))
	step = fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	step.Prepared = s.prepared
	fixture.RunStep(s.t, s.p.Ctx, peer, s.record, step)
	fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Exec("ROLLBACK", 0))
	held := predefinedHeld(s, "REINDEX INDEX ix")
	analyzeHeld := predefinedHeld(s, "ANALYZE t")
	s.actor("")
	s.exec("REVOKE pg_maintain FROM pdef_actor")
	s.actor("pdef_actor")
	fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.RoleReferenceError(held.SQL, "42501", "permission denied for index ix", "", false, "execute"))
	s.clearNotices()
	fixture.RunStep(s.t, s.p.Ctx, analyzeHeld, s.record, fixture.Exec(analyzeHeld.SQL, 0))
	s.notice("WARNING", "01000", `permission denied to analyze "t", skipping it`)
	s.clearNotices()
	s.actor("")
	predefinedSelect(s, "SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}})
	predefinedSelect(s, "SELECT indexname FROM pg_indexes WHERE tablename='t' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"ix"}})
}

func TestReferencePredefinedMaintain(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *membershipScenario, db *sql.DB) {
		predefinedMaintain(s, db)
		s.exec("CREATE SCHEMA pdef_private")
		s.exec("CREATE TABLE pdef_private.t(n INT)")
		s.run(fixture.Exec("INSERT INTO pdef_private.t VALUES(9),(9),(10)", 3))
		s.exec("CREATE INDEX ix ON pdef_private.t(n)")
		s.exec("GRANT pg_maintain TO pdef_actor")
		s.actor("pdef_actor")
		predefinedFailure(s, "REINDEX TABLE pdef_private.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_private"}, false, "execute")
		predefinedFailure(s, "ANALYZE pdef_private.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_private"}, false, "execute")
		s.actor("")
		// No native schema-GRANT grammar: read-all's actual schema USAGE
		// consumer removes lookup denial without inventing that adjacent grammar.
		s.exec("GRANT pg_read_all_data TO pdef_actor")
		s.actor("pdef_actor")
		s.exec("REINDEX TABLE pdef_private.t")
		s.exec("REINDEX INDEX pdef_private.ix")
		s.exec("VACUUM pdef_private.t")
		s.clearNotices()
		s.exec("ANALYZE pdef_private.t")
		if len(*s.notices) != 0 {
			s.t.Fatalf("schema-authorized pg_maintain ANALYZE notices %#v", *s.notices)
		}
		predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='pdef_private.t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(3)}})
		if !s.backend.PG {
			predefinedFailure(s, "CLUSTER pdef_private.t USING ix", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"}, false, "prepare")
		}
		s.actor("")
	})
}

func TestReferencePredefinedMaintainFeatureSmoke(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, predefinedMaintain)
}
