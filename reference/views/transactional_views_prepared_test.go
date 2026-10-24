package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsPrepared(t *testing.T) {
	transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		base(fixture.Exec("CREATE TABLE txv_source(id INT)", 0))
		base(fixture.Exec("INSERT INTO txv_source VALUES(1),(2)", 2))
		base(fixture.Exec("CREATE VIEW txv_v AS SELECT id FROM txv_source", 0))
		idQuery, starQuery := "SELECT id FROM txv_v ORDER BY id", "SELECT * FROM txv_v ORDER BY id"
		idStmt, err := c.PrepareContext(p.Ctx, idQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer idStmt.Close()
		starStmt, err := c.PrepareContext(p.Ctx, starQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer starStmt.Close()
		idExec := fixture.HeldStatement{Executor: c, Statement: idStmt, SQL: idQuery}
		starExec := fixture.HeldStatement{Executor: c, Statement: starStmt, SQL: starQuery}
		read := func(x fixture.Executor, q string, values ...int64) {
			rows := make([][]any, 0, len(values))
			for _, v := range values {
				rows = append(rows, []any{v})
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, rows))
		}
		read(idExec, idQuery, 1, 2)
		read(starExec, starQuery, 1, 2)
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE OR REPLACE VIEW txv_v AS SELECT id+10 AS id FROM txv_source", 0))
		read(idExec, idQuery, 11, 12)
		read(starExec, starQuery, 11, 12)
		finish(false)
		if mode == "autocommit" {
			read(idExec, idQuery, 11, 12)
		} else {
			read(idExec, idQuery, 1, 2)
			read(starExec, starQuery, 1, 2)
		}
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE OR REPLACE VIEW txv_v AS SELECT id+20 AS id FROM txv_source", 0))
		finish(true)
		read(idExec, idQuery, 21, 22)
		read(starExec, starQuery, 21, 22)
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE OR REPLACE VIEW txv_v AS SELECT id+30 AS id,true AS flag FROM txv_source", 0))
		read(idExec, idQuery, 31, 32)
		if mode != "autocommit" {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("SAVEPOINT shape", 0))
		}
		bad := fixture.ImplicitError(starQuery, fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"})
		bad.SelectRows = true
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, starExec, b.Recorder, bad)
		if mode != "autocommit" {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("ROLLBACK TO shape", 0))
			finish(false)
			read(starExec, starQuery, 21, 22)
		} else {
			finish(true)
		}
		// Retained name-based SQL is not silently retargeted to an old identity.
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("ALTER VIEW txv_v RENAME TO txv_renamed", 0))
		missing := fixture.ImplicitError(idQuery, fixture.Diagnostic{Code: "42P01", Message: `relation "txv_v" does not exist`, Position: "16"})
		missing.SelectRows = true
		missing.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, idExec, b.Recorder, missing)
		if mode != "autocommit" {
			finish(false)
			read(idExec, idQuery, 21, 22)
		} else {
			finish(false)
		}
	})
}
