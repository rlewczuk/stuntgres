package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_reset_user", "pgss_reset_group"})
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	pgssCase(t, true, func(s *pgssScenario) {
		pgssResetSeed(s)
		pgssResetPrivileges(s, true, &databases)
	})
}

// Shared with the actual smoke, not a test invocation or a backend shortcut.
func pgssResetPrivileges(s *pgssScenario, nullBoundary bool, databases *[]string) {
	s.t.Helper()
	s.exec("CREATE ROLE pgss_reset_user")
	s.exec("CREATE ROLE pgss_reset_group")
	s.exec("GRANT USAGE ON SCHEMA pgss_part TO pgss_reset_user")
	issuer, err := s.b.DB.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer issuer.Close()
	run := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, issuer, s.b.Recorder, step) }
	run(fixture.Exec("SET pg_stat_statements.track=none", 0))
	run(fixture.Exec("SET ROLE pgss_reset_user", 0))
	defer issuer.ExecContext(s.p.Ctx, "RESET ROLE")
	denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pg_stat_statements_reset")
	before, info := pgssResetSnapshot(s), pgssResetInfo(s)
	checkDenied := func(q string, x fixture.Executor, args ...any) {
		step := fixture.ErrorDiagnostic(q, denied, args...)
		step.SelectRows, step.ErrorStage = true, "execute"
		fixture.RunStep(s.t, s.p.Ctx, x, s.b.Recorder, step)
		pgssResetEqual(s.t, pgssResetSnapshot(s), before)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
	}
	checkDenied(pgssResetCall, issuer)
	stmt, err := issuer.PrepareContext(s.p.Ctx, pgssResetCall)
	if err != nil {
		s.t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: issuer, Statement: stmt, SQL: pgssResetCall}
	checkDenied(pgssResetCall, held)
	s.exec("GRANT pg_read_all_stats TO pgss_reset_user")
	checkDenied(pgssResetCall, held)
	if nullBoundary {
		s.exec("CREATE TABLE pgss_part.pgss_reset_acl_null(u oid,d oid,q bigint,m boolean)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_reset_acl_null VALUES(NULL,0,0,false),(0,NULL,0,false),(0,0,NULL,false),(0,0,0,NULL)", 4))
		s.exec("GRANT SELECT ON pgss_part.pgss_reset_acl_null TO pgss_reset_user")
		for slot, typ := range []string{"oid", "oid", "bigint", "boolean"} {
			args := []string{"0", "0", "0", "false"}
			args[slot] = "NULL::" + typ
			q := "SELECT pgss_part.pg_stat_statements_reset(" + strings.Join(args, ",") + ")"
			// REL_18_6 strict constant-NULL pruning removes executor ACL.
			pgssResetScalar(s, issuer, q, true)
			column := []string{"u", "d", "q", "m"}[slot]
			q = "SELECT pgss_part.pg_stat_statements_reset(u,d,q,m) FROM pgss_part.pgss_reset_acl_null WHERE " + column + " IS NULL"
			checkDenied(q, issuer)
			args[slot] = "$1::" + typ
			q = "SELECT pgss_part.pg_stat_statements_reset(" + strings.Join(args, ",") + ")"
			run(fixture.Exec("SET plan_cache_mode=force_custom_plan", 0))
			custom, err := issuer.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			pgssResetScalar(s, fixture.HeldStatement{Executor: issuer, Statement: custom, SQL: q}, q, true, nil)
			custom.Close()
			run(fixture.Exec("SET plan_cache_mode=force_generic_plan", 0))
			generic, err := issuer.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			checkDenied(q, fixture.HeldStatement{Executor: issuer, Statement: generic, SQL: q}, nil)
			generic.Close()
		}
		pgssResetEqual(s.t, pgssResetSnapshot(s), before)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
	}
	// Inherited EXECUTE is real authority, not statistics-text visibility.
	s.exec("GRANT EXECUTE ON FUNCTION " + pgssResetSignature + " TO pgss_reset_group")
	s.exec("GRANT pgss_reset_group TO pgss_reset_user")
	// EXECUTE authorizes explicit filters for another user's other database;
	// it is not restricted to the current principal/database.
	if s.b.PG {
		*databases = append(*databases, "pgss_reset_acl_peer")
	}
	s.exec("CREATE DATABASE pgss_reset_acl_peer")
	pool, _, record := fixture.DatabaseScenarioPool(s.t, s.p, s.b.PG, "pgss_reset_acl_peer", "")
	peer, err := pool.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer peer.Close()
	for _, q := range []string{"SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track=top"} {
		fixture.RunStep(s.t, s.p.Ctx, peer, record, fixture.Exec(q, 0))
	}
	fixture.RunStep(s.t, s.p.Ctx, peer, record, fixture.Select("SELECT 83::bigint AS pgss_reset_acl_peer", []string{"pgss_reset_acl_peer"}, []string{"INT8"}, [][]any{{int64(83)}}))
	var foreignUser, foreignDB, foreignQuery int64
	if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT userid::bigint,dbid::bigint,queryid FROM pgss_part.pg_stat_statements WHERE query LIKE 'SELECT % AS pgss_reset_acl_peer'").Scan(&foreignUser, &foreignDB, &foreignQuery); err != nil {
		s.t.Fatal(err)
	}
	before = pgssResetSnapshot(s)
	var nonmatches [][]any
	for _, row := range before {
		if row[3] != foreignQuery {
			nonmatches = append(nonmatches, row)
		}
	}
	filtered := "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint)"
	pgssResetScalar(s, issuer, filtered, false, foreignUser, foreignDB, foreignQuery)
	pgssResetEqual(s.t, pgssResetSnapshot(s), nonmatches)
	pgssResetScalar(s, held, pgssResetCall, false)
	pgssResetEqual(s.t, pgssResetSnapshot(s), [][]any(nil))
	// Recreate foreign-principal data, then revoke without re-preparing.
	s.work(fixture.Select("SELECT k AS pgss_reset_revoke FROM pgss_part.pgss_reset_data", []string{"pgss_reset_revoke"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	before, info = pgssResetSnapshot(s), pgssResetInfo(s)
	s.exec("REVOKE pgss_reset_group FROM pgss_reset_user")
	checkDenied(pgssResetCall, held)
	// Direct grant also permits reset; do not hard-code superuser-only body.
	s.exec("GRANT EXECUTE ON FUNCTION " + pgssResetSignature + " TO pgss_reset_user")
	pgssResetScalar(s, held, pgssResetCall, false)
	pgssResetEqual(s.t, pgssResetSnapshot(s), [][]any(nil))
	s.exec("REVOKE EXECUTE ON FUNCTION " + pgssResetSignature + " FROM pgss_reset_user")
	before, info = pgssResetSnapshot(s), pgssResetInfo(s)
	checkDenied(pgssResetCall, held)
	s.exec("ALTER FUNCTION " + pgssResetSignature + " OWNER TO pgss_reset_user")
	pgssResetScalar(s, held, pgssResetCall, false)
}
