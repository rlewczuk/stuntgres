package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWTransactions(t *testing.T) {
	p, provider := postgresFDWPhysicalScenario(t)
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWPhysicalInstall(t, b, run)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted"."data table" SET remote_label='x' WHERE "Remote ID"=1`, 1))
		q := "SELECT id,label,flag,bytes FROM pf_table WHERE id=$1 ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		one := func(label string) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, externalSQLProviderRows(b.PG, q, [][]any{{int64(1), label, true, "\x00\xff"}}, int64(1)))
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Select("SELECT isolation FROM pf_identity", []string{"isolation"}, []string{"TEXT"}, [][]any{{"repeatable read"}}))
		one("x")
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted"."data table" SET remote_label='outside' WHERE "Remote ID"=1`, 1))
		one("x")
		run(fixture.Exec("SAVEPOINT pf_keep", 0))
		run(fixture.Exec("CREATE TABLE pf_local(v integer)", 0))
		run(fixture.Exec("INSERT INTO pf_local VALUES(73)", 1))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT pf_keep", 0))
		run(fixture.Exec("RELEASE SAVEPOINT pf_keep", 0))
		one("x")
		run(fixture.Exec("COMMIT", 0))
		one("outside")
		run(fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(fixture.Select("SELECT isolation FROM pf_identity", []string{"isolation"}, []string{"TEXT"}, [][]any{{"serializable"}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, externalSQLProviderRows(b.PG, q, [][]any{{int64(2), nil, false, nil}, {int64(2), nil, false, nil}}, int64(2)))
		run(fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, externalSQLProviderRows(b.PG, q, nil, nil))
		one("outside")
		// A real remote division error is recovered through the local explicit
		// savepoint; no partial local INSERT survives the failed statement.
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`CREATE VIEW "pf source ""quoted".bad AS SELECT 1/(unused-92) AS bad FROM "pf source ""quoted"."data table"`, 0))
		defer postgresFDWRemoteCleanup(t, p, c, `DROP VIEW IF EXISTS "pf source ""quoted".bad`)
		run(fixture.Exec(`CREATE FOREIGN TABLE pf_bad(bad integer) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'bad')`, 0))
		run(fixture.Exec("CREATE TABLE pf_local(v integer)", 0))
		run(fixture.Exec("INSERT INTO pf_local VALUES(73)", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT pf_error", 0))
		postgresFDWRemoteError(t, p, c, "INSERT INTO pf_local SELECT bad FROM pf_bad", prepared, "22012", "division by zero", `remote SQL command: SELECT bad FROM "pf source ""quoted".bad`)
		run(fixture.Exec("ROLLBACK TO SAVEPOINT pf_error", 0))
		run(fixture.Select("SELECT v FROM pf_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
		one("outside")
		run(fixture.Exec("RELEASE SAVEPOINT pf_error", 0))
		run(fixture.Exec("COMMIT", 0))
		postgresFDWCancelScan(t, p, b, c, prepared, provider)
	})
}

func postgresFDWRemoteError(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, prepared bool, code, message, remoteContext string) {
	t.Helper()
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(p.Ctx, q)
		if prepareErr != nil {
			t.Fatal("remote failure during Prepare", prepareErr)
		}
		defer stmt.Close()
		_, err = stmt.ExecContext(p.Ctx)
	} else {
		_, err = c.ExecContext(p.Ctx, q)
	}
	want := fdwDiagnostic(code, message)
	want.Context = remoteContext
	fixture.AssertDiagnostic(t, err, &want)
}

func postgresFDWCancelScan(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, provider *fixture.ExternalSQLProvider) {
	t.Helper()
	defer postgresFDWRemoteCleanup(t, p, c, `DROP VIEW IF EXISTS "pf source ""quoted".blocked`)
	const key int64 = 718137
	externalSQLLock(t, p, key)
	fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`CREATE VIEW "pf source ""quoted".blocked AS SELECT 1 AS id FROM pg_catalog.pg_advisory_xact_lock(718137)`, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE FOREIGN TABLE pf_blocked(id integer) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'blocked',fetch_size '1')`, 0))
	pid := postgresFDWPID(t, p, c)
	const q = "SELECT id FROM pf_blocked"
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	var failure error
	var returnedRow bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		var rows *sql.Rows
		if stmt == nil {
			rows, failure = c.QueryContext(ctx, q)
		} else {
			rows, failure = stmt.QueryContext(ctx)
		}
		if rows != nil {
			returnedRow = rows.Next()
			if failure == nil {
				failure = rows.Err()
			}
			rows.Close()
		}
	}()
	defer func() {
		cancel()
		cleanup, end := context.WithTimeout(context.Background(), fixture.Liveness)
		defer end()
		_, _ = fixture.Admin.ExecContext(cleanup, "SELECT pg_cancel_backend($1)", pid)
		select {
		case <-done:
		case <-cleanup.Done():
			t.Error("postgres_fdw canceled worker failed to join")
		}
	}()
	externalSQLWaitBlocked(t, p.Ctx, pid)
	if b.PG {
		var canceled bool
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
			t.Fatal("actual remote cancellation", canceled, err)
		}
	} else {
		wires := provider.Snapshot()
		if len(wires) == 0 {
			t.Fatal("native cancellation has no real transport")
		}
		if err := wires[len(wires)-1].Cancel(p.Ctx); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-done:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	if returnedRow {
		t.Fatal("canceled foreign scan returned a row")
	}
	want := fdwDiagnostic("57014", "canceling statement due to user request")
	want.Context = `remote SQL command: SELECT id FROM "pf source ""quoted".blocked`
	fixture.AssertDiagnostic(t, failure, &want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pf_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
}
