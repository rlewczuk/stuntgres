package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const returnNextMigration577 = `CREATE OR REPLACE FUNCTION acquire_external_auth_link_refresh_lease(arg_provider_id text, arg_user_id uuid, timeout_ms bigint)
RETURNS SETOF external_auth_links AS $$
DECLARE r external_auth_links;
BEGIN
	UPDATE external_auth_links
	SET
		refresh_lease_expires_at = NOW() + (timeout_ms || ' ms')::interval
	WHERE
		provider_id = arg_provider_id
		AND user_id = arg_user_id
		AND (refresh_lease_expires_at IS NULL OR refresh_lease_expires_at < NOW())
	RETURNING * INTO r;
	-- Got the lease, return the one row.
	IF FOUND THEN
		RETURN NEXT r;
		RETURN;
	END IF;
	-- Differentiate between unable to get the lease and the row being gone.
	IF EXISTS (SELECT 1 FROM external_auth_links WHERE provider_id = arg_provider_id AND user_id = arg_user_id) THEN
		RAISE EXCEPTION 'row is currently leased by another replica'
			USING ERRCODE = 'check_violation',
				CONSTRAINT = 'external_auth_link_active_lease';
	END IF;
	-- Row is gone, return nothing.
	RETURN;
END;
$$ LANGUAGE plpgsql;`

const returnNextLeaseQuery = "SELECT provider_id,user_id::text AS user_id,refresh_lease_expires_at IS NOT NULL AS has_expiry,refresh_lease_expires_at>NOW() AS future FROM acquire_external_auth_link_refresh_lease($1::text,$2::uuid,$3::bigint)"
const returnNextUser = "00000000-0000-0000-0000-000000000001"

func returnNextMigrationSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c, "CREATE TABLE external_auth_links(provider_id text NOT NULL,user_id uuid NOT NULL,refresh_lease_expires_at timestamptz,PRIMARY KEY(provider_id,user_id))", returnNextMigration577)
}

func returnNextLeaseCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, executor fixture.Executor, existingRows int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM external_auth_links", existingRows))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO external_auth_links(provider_id,user_id) VALUES('test','"+returnNextUser+"')", 1))
	userOperatorExec(t, p, b, c, "BEGIN")
	s := sqlExecutionStep(b, returnNextLeaseQuery, []string{"provider_id", "user_id", "has_expiry", "future"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"test", returnNextUser, true, true}}, "test", returnNextUser, int64(60000))
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
	userOperatorExec(t, p, b, c, "COMMIT")
	var priorExpiry string
	if err := c.QueryRowContext(p.Ctx, "SELECT refresh_lease_expires_at::text FROM external_auth_links WHERE provider_id='test'").Scan(&priorExpiry); err != nil {
		t.Fatal(err)
	}
	d := fixture.ControlsReferenceDiagnostic("23514", "row is currently leased by another replica")
	d.Constraint = "external_auth_link_active_lease"
	d.Context = "PL/pgSQL function acquire_external_auth_link_refresh_lease(text,uuid,bigint) line 19 at RAISE"
	s = fixture.ErrorDiagnostic(returnNextLeaseQuery, d, "test", returnNextUser, int64(60000))
	s.SelectRows = true
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
	sqlExecutionBoth(t, p, b, c, "SELECT refresh_lease_expires_at=$1::timestamptz AS v FROM external_auth_links WHERE provider_id='test'", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, priorExpiry)
	count := sqlExecutionStep(b, "SELECT count(*) AS v FROM external_auth_links WHERE refresh_lease_expires_at>NOW()", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}})
	count.CheckMetadata = sqlExecutionMetadata(b, []string{"v"}, []string{"INT8"}, false)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, count)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(count))
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, sqlExecutionStep(b, returnNextLeaseQuery, []string{"provider_id", "user_id", "has_expiry", "future"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, nil, "missing", returnNextUser, int64(60000)))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE external_auth_links SET refresh_lease_expires_at=NOW()-interval '1 second'", 1))
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, sqlExecutionStep(b, returnNextLeaseQuery, []string{"provider_id", "user_id", "has_expiry", "future"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"test", returnNextUser, true, true}}, "test", returnNextUser, int64(60000)))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE external_auth_links SET refresh_lease_expires_at=NULL", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEMP TABLE rn_acquired AS SELECT * FROM acquire_external_auth_link_refresh_lease('test','"+returnNextUser+"',60000)", 1))
	sqlExecutionBoth(t, p, b, c, "SELECT a.refresh_lease_expires_at=e.refresh_lease_expires_at AS v FROM rn_acquired a JOIN external_auth_links e USING(provider_id,user_id)", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
	userOperatorExec(t, p, b, c, "DROP TABLE rn_acquired")
}

func TestReferencePLpgSQLReturnNextMigration(t *testing.T) {
	t.Run("original577_lease_branches", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			returnNextMigrationSetup(t, p, b, c)
			stmt, err := c.PrepareContext(p.Ctx, returnNextLeaseQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			returnNextLeaseCheck(t, p, b, c, c, 0)
			returnNextLeaseCheck(t, p, b, c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: returnNextLeaseQuery}, 1)
		})
	})
}
