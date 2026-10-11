package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func updateCurrentOfPoolPersistence(t *testing.T) {
	updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		currentOfSeed(run)
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
		run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
		run(fixture.Exec("UPDATE t SET v='persistent' WHERE CURRENT OF c", 1))
		run(fixture.Exec("COMMIT", 0))
		expected := fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "persistent"}, {int64(2), "two"}, {int64(3), "three"}})
		run(expected)
		if b.PG {
			return
		} // The SQL persistence phase above is shared; host ownership is native.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		dsn := t.Name() + "/opaque"
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		restored := sql.OpenDB(connector)
		if err = stuntgres.Restore(p.Ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
			restored.Close()
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, restored, b.Recorder, expected)
		if err = restored.Close(); err != nil {
			t.Fatal(err)
		}
		connector, err = driver.OpenConnector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		reopened := sql.OpenDB(connector)
		defer reopened.Close()
		fixture.RunStep(t, p.Ctx, reopened, b.Recorder, expected)
		isolatedConnector, err := stuntgres.NewDriver().OpenConnector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		isolated := sql.OpenDB(isolatedConnector)
		defer isolated.Close()
		rows, err := isolated.QueryContext(p.Ctx, "SELECT id,v FROM t ORDER BY id")
		if rows != nil {
			rows.Close()
			t.Fatal("fresh Driver borrowed restored catalog")
		}
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "t" does not exist`, Position: "18"})
		fixture.DeleteReferenceExecuteStep(t, p, reopened, b.Recorder, currentOfError("UPDATE t SET v='bad' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
		fixture.RunStep(t, p.Ctx, reopened, b.Recorder, expected)
	})
}
