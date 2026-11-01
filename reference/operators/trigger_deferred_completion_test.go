package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func deferredTerminalControls(t *testing.T) {
	t.Run("driver-commit-and-chain", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"))
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1)))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, deferredCount("audit", 0))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(1), "c"}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(fixture.Exec("COMMIT AND CHAIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			run(deferredCount("audit", 1))
			run(fixture.Exec("COMMIT", 0))
			run(deferredCount("audit", 2))
		})
	})
	t.Run("prepare-transaction-settles-bodies", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE FUNCTION d_prepared_helper() RETURNS integer LANGUAGE SQL AS 'SELECT 7'", "CREATE FUNCTION d_prepare() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,'failed'); PERFORM 1/0; RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_prepare()")
			gid := "deferred_user_prepare"
			fixture.PreparedReferenceCleanup(t, b, gid)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			run(deferredFailure("PREPARE TRANSACTION 'deferred_user_prepare'", "d_prepare"))
			run(deferredCount("d", 0))
			run(deferredCount("audit", 0))
			fixture.PreparedReferenceAbsent(t, p, c, gid)
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION d_prepare() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,(SELECT count(*) FROM d),'prepared'); CREATE OR REPLACE FUNCTION d_prepared_helper() RETURNS integer LANGUAGE SQL AS 'SELECT 99'; RETURN NULL; END$$")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1)))
			run(deferredCount("audit", 0))
			run(fixture.Exec("PREPARE TRANSACTION 'deferred_user_prepare'", 0))
			run(deferredCount("d", 0))
			run(deferredCount("audit", 0))
			run(fixture.Select("SELECT d_prepared_helper() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("COMMIT PREPARED 'deferred_user_prepare'", 0))
			run(deferredAudit([]any{"INSERT", int64(2), nil, int64(20), int64(1), "prepared"}))
			run(fixture.Prepared(fixture.Select("SELECT d_prepared_helper() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})))
			fixture.PreparedReferenceAbsent(t, p, c, gid)
		})
	})
}
