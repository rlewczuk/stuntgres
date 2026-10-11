package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func pgSequencesCurrentAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Run("CurrentACLAndJoinedNames", func(t *testing.T) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		owner := pgSequencesOwner(t, p, peer)
		defer a.ExecContext(context.Background(), "ROLLBACK")
		defer a.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		pgSequenceExec(t, p, b, peer, "CREATE ROLE pgsv_rr_actor")
		defer peer.ExecContext(context.Background(), "DROP ROLE pgsv_rr_actor")
		pgSequenceExec(t, p, b, peer, "CREATE SCHEMA pgsv_rr_schema")
		defer peer.ExecContext(context.Background(), "DROP SCHEMA pgsv_rr_schema CASCADE")
		pgSequenceExec(t, p, b, peer, "CREATE SEQUENCE pgsv_rr_acl")
		defer peer.ExecContext(context.Background(), "DROP SEQUENCE IF EXISTS public.pgsv_rr_acl")
		defer func() {
			a.ExecContext(context.Background(), "ROLLBACK")
			a.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		}()
		pgSequencesScalar(t, p, b, peer, "SELECT setval('pgsv_rr_acl',42,true)", "setval", 42)
		pgSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION pgsv_rr_actor")
		pgSequenceExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT s.seqstart FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid WHERE c.relname='pgsv_rr_acl'", []string{"seqstart"}, []string{"INT8"}, [][]any{{int64(1)}}))
		q := "SELECT schemaname,sequenceowner,last_value FROM pg_sequences WHERE sequencename='pgsv_rr_acl'"
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select(q, []string{"schemaname", "sequenceowner", "last_value"}, []string{"NAME", "NAME", "INT8"}, [][]any{{"public", owner, nil}}))
		pgSequenceExec(t, p, b, peer, "GRANT pg_read_all_data TO pgsv_rr_actor")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select(q, []string{"schemaname", "sequenceowner", "last_value"}, []string{"NAME", "NAME", "INT8"}, [][]any{{"public", owner, int64(42)}}))
		pgSequenceExec(t, p, b, peer, "REVOKE pg_read_all_data FROM pgsv_rr_actor")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select(q, []string{"schemaname", "sequenceowner", "last_value"}, []string{"NAME", "NAME", "INT8"}, [][]any{{"public", owner, nil}}))
		pgSequenceExec(t, p, b, a, "ROLLBACK")
		pgSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
		pgSequenceExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT s.seqstart FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid WHERE c.relname='pgsv_rr_acl'", []string{"seqstart"}, []string{"INT8"}, [][]any{{int64(1)}}))
		pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgsv_rr_acl SET SCHEMA pgsv_rr_schema")
		pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgsv_rr_schema.pgsv_rr_acl OWNER TO pgsv_rr_actor")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select(q, []string{"schemaname", "sequenceowner", "last_value"}, []string{"NAME", "NAME", "INT8"}, [][]any{{"public", owner, int64(42)}}))
		pgSequenceExec(t, p, b, a, "ROLLBACK")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select(q, []string{"schemaname", "sequenceowner", "last_value"}, []string{"NAME", "NAME", "INT8"}, [][]any{{"pgsv_rr_schema", "pgsv_rr_actor", int64(42)}}))
	})
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "PrepareInsideRR", true: "PrepareBeforeRR"}[before], func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.ExecContext(context.Background(), "ROLLBACK")
			pgSequenceExec(t, p, b, peer, "CREATE SEQUENCE pgsv_rr_prepare START 15 CACHE 9")
			defer peer.ExecContext(context.Background(), "DROP SEQUENCE pgsv_rr_prepare")
			q := "SELECT start_value,cache_size,last_value FROM pg_sequences WHERE sequencename='pgsv_rr_prepare'"
			var stmt *sql.Stmt
			var err error
			if before {
				stmt, err = a.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
			}
			pgSequenceExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			if !before {
				stmt, err = a.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
			}
			defer stmt.Close()
			defer a.ExecContext(context.Background(), "ROLLBACK")
			pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgsv_rr_prepare START 21 CACHE 4")
			wantStart, wantCache := int64(15), int64(9)
			if before {
				wantStart, wantCache = 21, 4
			}
			fixture.PgDescriptionExecutorRows(t, p, b, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}, fixture.Select(q, []string{"start_value", "cache_size", "last_value"}, []string{"INT8", "INT8", "INT8"}, [][]any{{wantStart, wantCache, nil}}))
		})
	}
}

func pgSequencesDeniedLockOracle(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
	t.Run("DeniedHelperStillRX", func(t *testing.T) {
		pgSequenceExec(t, p, b, peer, "CREATE ROLE pgsv_lock_actor")
		defer peer.ExecContext(context.Background(), "DROP ROLE pgsv_lock_actor")
		pgSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION pgsv_lock_actor")
		defer a.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		pgSequenceExec(t, p, b, a, "BEGIN")
		defer a.ExecContext(context.Background(), "ROLLBACK")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT last_value FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}))
		pid := fixture.LockPID(t, p.Ctx, a)
		var held bool
		if err := peer.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='pgsv_demand_a'::regclass AND mode='RowExclusiveLock' AND granted)", pid).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if !held {
			t.Fatal("denied helper must open/lock before readability test")
		}
		pgSequenceExec(t, p, b, a, "ROLLBACK")
		pgSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
	})
}
