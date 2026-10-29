package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTcnBindingsControls(t *testing.T) { tcnBindingsScenario(t) }
func TestReferenceTcnFeatureSmoke(t *testing.T) {
	sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		sender := sessionMessageConn(t, p, b)
		receiver := sessionMessageConn(t, p, b)
		run := func(q string, n int64) { fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec(q, n)) }
		run("CREATE EXTENSION tcn", 0)
		run("CREATE TABLE tcn_live(caption text,id int,PRIMARY KEY(id,caption))", 0)
		run(`CREATE TRIGGER changes AFTER INSERT OR UPDATE OR DELETE ON tcn_live FOR EACH ROW EXECUTE FUNCTION triggered_change_notification('Tcn.Live')`, 0)
		sessionMessageSQL(t, p, b, receiver, `LISTEN "Tcn.Live"`)
		run("INSERT INTO tcn_live VALUES ('O''Brien',1)", 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",I,"id"='1',"caption"='O''Brien'`)
		stmt, err := sender.PrepareContext(p.Ctx, "UPDATE tcn_live SET id=$1 WHERE id=$2")
		if err != nil {
			t.Fatal(err)
		}
		result, err := stmt.ExecContext(p.Ctx, 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal("prepared count", n, err)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",U,"id"='1',"caption"='O''Brien'`)
		run("BEGIN", 0)
		run("INSERT INTO tcn_live VALUES ('rolled',4)", 1)
		run("ROLLBACK", 0)
		run("INSERT INTO tcn_live VALUES ('marker',5)", 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",I,"id"='5',"caption"='marker'`)
		run("DELETE FROM tcn_live WHERE id=2", 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",D,"id"='2',"caption"='O''Brien'`)
		run("INSERT INTO tcn_live VALUES ('disabled',3)", 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",I,"id"='3',"caption"='disabled'`)
		if !b.PG {
			tcnRestoreLive(t, p, b)
		}
	})
}

func tcnBindingsScenario(t *testing.T) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "tcn_home")
	fixture.SchemaReferenceCleanup(t, "tcn_moved")
	sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		sender, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer sender.Close()
		receiver := sessionMessageConn(t, p, b)
		run := func(q string, n int64) { fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec(q, n)) }
		run("CREATE SCHEMA tcn_home", 0)
		run("CREATE SCHEMA tcn_moved", 0)
		run("CREATE EXTENSION tcn SCHEMA tcn_home", 0)
		run(`CREATE TABLE tcn_live(label text,id int,PRIMARY KEY(id,label))`, 0)
		run(`CREATE TRIGGER changes AFTER INSERT OR UPDATE OR DELETE ON tcn_live FOR EACH ROW WHEN (pg_trigger_depth() = 0) EXECUTE FUNCTION tcn_home.triggered_change_notification('Tcn.Live')`, 0)
		sessionMessageSQL(t, p, b, receiver, `LISTEN "Tcn.Live"`)
		const update = "UPDATE tcn_live SET id=$1 WHERE id=$2"
		held, err := sender.PrepareContext(p.Ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(`INSERT INTO tcn_live VALUES ('O''Brien',1)`, 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",I,"id"='1',"label"='O''Brien'`)
		run("ALTER FUNCTION tcn_home.triggered_change_notification() RENAME TO real_changes", 0)
		run("ALTER EXTENSION tcn SET SCHEMA tcn_moved", 0)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: sender, Statement: held, SQL: update}, b.Recorder, fixture.Exec(update, 1, 2, 1))
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",U,"id"='1',"label"='O''Brien'`)
		run("CREATE FUNCTION public.triggered_change_notification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$", 0)
		run("CREATE TABLE tcn_decoy(id int PRIMARY KEY)", 0)
		run("CREATE TRIGGER decoy AFTER INSERT ON tcn_decoy FOR EACH ROW EXECUTE FUNCTION public.triggered_change_notification()", 0)
		run("INSERT INTO tcn_decoy VALUES (1)", 1)
		run("ALTER TABLE tcn_live DISABLE TRIGGER changes", 0)
		run("INSERT INTO tcn_live VALUES ('disabled',3)", 1)
		run("ALTER TABLE tcn_live ENABLE TRIGGER changes", 0)
		run("BEGIN", 0)
		run("ALTER TABLE tcn_live DISABLE TRIGGER changes", 0)
		run("ROLLBACK", 0)
		run("BEGIN", 0)
		run("INSERT INTO tcn_live VALUES ('rolled',4)", 1)
		run("ROLLBACK", 0)
		run("INSERT INTO tcn_live VALUES ('marker',5)", 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",I,"id"='5',"label"='marker'`)
		run("DELETE FROM tcn_live WHERE id=2", 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",D,"id"='2',"label"='O''Brien'`)
		fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Select("SELECT id,label FROM tcn_live ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "disabled"}, {int64(5), "marker"}}))
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		run("ALTER TABLE tcn_live RENAME COLUMN label TO caption", 0)
		run("UPDATE tcn_live SET id=id WHERE id=5", 1)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", `"tcn_live",U,"id"='5',"caption"='marker'`)
		if !b.PG {
			tcnRestoreLive(t, p, b)
		}
	})
}

func tcnRestoreLive(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector("tcn-restored")
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	defer raw.Close()
	db, recorder := fixture.RecordedNativePool(t, connector)
	if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	receiver, err := raw.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	fixture.RunStep(t, p.Ctx, sender, recorder, fixture.Select(
		"SELECT pg_listening_channels()", []string{"pg_listening_channels"}, []string{"TEXT"}, nil,
	))
	fixture.RunStep(t, p.Ctx, sender, recorder, fixture.Select(
		"SELECT id,caption FROM tcn_live ORDER BY id", []string{"id", "caption"}, []string{"INT4", "TEXT"},
		[][]any{{int64(3), "disabled"}, {int64(5), "marker"}},
	))
	if _, err := receiver.ExecContext(p.Ctx, `LISTEN "Tcn.Live"`); err != nil {
		t.Fatal(err)
	}
	for i, q := range []string{"INSERT INTO tcn_live VALUES ('fresh',6)", "UPDATE tcn_live SET id=7 WHERE id=6", "DELETE FROM tcn_live WHERE id=7"} {
		fixture.RunStep(t, p.Ctx, sender, recorder, fixture.Exec(q, 1))
		payload := []string{`"tcn_live",I,"id"='6',"caption"='fresh'`, `"tcn_live",U,"id"='6',"caption"='fresh'`, `"tcn_live",D,"id"='7',"caption"='fresh'`}[i]
		sessionMessageReceive(t, p, b, receiver, "Tcn.Live", payload)
	}
}
