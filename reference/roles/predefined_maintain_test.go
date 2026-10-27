package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func predefinedMaintain(s *fixture.RoleMembershipScenario, db *sql.DB) {
	s.T.Helper()
	s.Exec("CREATE TABLE t(n INT)")
	s.Run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
	s.Exec("CREATE INDEX ix ON t(n)")
	initialTuples := float64(-1)
	if s.Backend.PG {
		// PostgreSQL's CREATE INDEX heap scan has already published the
		// population; native index creation does not collect maintenance facts.
		initialTuples = 1
	}
	predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{initialTuples}})
	s.Actor("pdef_actor")
	predefinedDenied(s, "REINDEX INDEX ix", "permission denied for index ix", false)
	predefinedDenied(s, "REINDEX TABLE t", "permission denied for table t", false)
	s.ClearNotices()
	s.Exec("ANALYZE t")
	s.Notice("WARNING", "01000", `permission denied to analyze "t", skipping it`)
	s.ClearNotices()
	predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{initialTuples}})
	s.Actor("")
	s.Exec("GRANT pg_maintain TO pdef_actor")
	s.Actor("pdef_actor")
	s.Exec("REINDEX INDEX ix")
	s.Exec("REINDEX TABLE t")
	s.ClearNotices()
	s.Exec("ANALYZE t")
	if len(*s.Notices) != 0 {
		s.T.Fatalf("pg_maintain ANALYZE notices %#v", *s.Notices)
	}
	predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(1)}})
	predefinedDenied(s, "SELECT n FROM t", "permission denied for table t", true)
	predefinedDenied(s, "DROP INDEX ix", "must be owner of index ix", false)
	predefinedDenied(s, "ALTER INDEX ix RENAME TO denied", "must be owner of index ix", false)
	s.Fail("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks", "")
	peer, err := db.Conn(s.P.Ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	defer peer.Close()
	defer peer.ExecContext(s.P.Ctx, "ROLLBACK")
	defer s.C.ExecContext(s.P.Ctx, "ROLLBACK")
	s.Exec("BEGIN")
	s.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Exec("BEGIN", 0))
	step := fixture.RoleReferenceError("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`, "", s.Prepared, "execute")
	fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, step)
	fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Exec("ROLLBACK", 0))
	s.Exec("ROLLBACK")
	fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Exec("BEGIN", 0))
	step = fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	step.Prepared = s.Prepared
	fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, step)
	fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Exec("ROLLBACK", 0))
	held := predefinedHeld(s, "REINDEX INDEX ix")
	analyzeHeld := predefinedHeld(s, "ANALYZE t")
	s.Actor("")
	s.Exec("REVOKE pg_maintain FROM pdef_actor")
	s.Actor("pdef_actor")
	fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.RoleReferenceError(held.SQL, "42501", "permission denied for index ix", "", false, "execute"))
	s.ClearNotices()
	fixture.RunStep(s.T, s.P.Ctx, analyzeHeld, s.Record, fixture.Exec(analyzeHeld.SQL, 0))
	s.Notice("WARNING", "01000", `permission denied to analyze "t", skipping it`)
	s.ClearNotices()
	s.Actor("")
	predefinedSelect(s, "SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}})
	predefinedSelect(s, "SELECT indexname FROM pg_indexes WHERE tablename='t' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"ix"}})
}

func TestReferencePredefinedMaintain(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		predefinedMaintain(s, db)
		s.Exec("CREATE SCHEMA pdef_private")
		s.Exec("CREATE TABLE pdef_private.t(n INT)")
		s.Run(fixture.Exec("INSERT INTO pdef_private.t VALUES(9),(9),(10)", 3))
		s.Exec("CREATE INDEX ix ON pdef_private.t(n)")
		s.Exec("GRANT pg_maintain TO pdef_actor")
		s.Actor("pdef_actor")
		predefinedFailure(s, "REINDEX TABLE pdef_private.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_private"}, false, "execute")
		predefinedFailure(s, "ANALYZE pdef_private.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_private"}, false, "execute")
		s.Actor("")
		// No native schema-GRANT grammar: read-all's actual schema USAGE
		// consumer removes lookup denial without inventing that adjacent grammar.
		s.Exec("GRANT pg_read_all_data TO pdef_actor")
		s.Actor("pdef_actor")
		s.Exec("REINDEX TABLE pdef_private.t")
		s.Exec("REINDEX INDEX pdef_private.ix")
		s.Exec("VACUUM pdef_private.t")
		s.ClearNotices()
		s.Exec("ANALYZE pdef_private.t")
		if len(*s.Notices) != 0 {
			s.T.Fatalf("schema-authorized pg_maintain ANALYZE notices %#v", *s.Notices)
		}
		predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='pdef_private.t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(3)}})
		if !s.Backend.PG {
			predefinedFailure(s, "CLUSTER pdef_private.t USING ix", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"}, false, "prepare")
		}
		s.Actor("")
	})
}

func TestReferencePredefinedMaintainFeatureSmoke(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, predefinedMaintain)
}
