package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func insertUsernameControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	insertUsernameSetup(t, p, b, c)
	run := insertUsernameRun(t, p, b, c)
	insert := "INSERT INTO iu_data VALUES($1,$2,$3) RETURNING actor"
	i, err := c.PrepareContext(p.Ctx, insert)
	if err != nil { t.Fatal(err) }
	defer i.Close()
	ih := fixture.HeldStatement{Executor: c, Statement: i, SQL: insert}
	update := "UPDATE iu_data SET note=$2 WHERE id=$1 RETURNING actor"
	u, err := c.PrepareContext(p.Ctx, update)
	if err != nil { t.Fatal(err) }
	defer u.Close()
	uh := fixture.HeldStatement{Executor: c, Statement: u, SQL: update}
	write := func(ex fixture.HeldStatement, q string, actor string, args ...any) { fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Select(q, []string{"actor"}, []string{"TEXT"}, [][]any{{actor}}, args...)) }
	run(fixture.Exec("SET search_path=public", 0))
	run(fixture.Exec("SET ROLE iu_writer", 0))
	run(fixture.Exec("INSERT INTO iu_data VALUES(1,NULL,NULL)", 1))
	write(ih, insert, "iu_writer", int64(2), "forged", "two")
	insertUsernameBoth(t, p, b, c, insertUsernameRows("SELECT id,actor,note FROM iu_data ORDER BY id", [][]any{{int64(1), "iu_writer", nil}, {int64(2), "iu_writer", "two"}}))
	run(fixture.Exec("SET ROLE iu_other", 0))
	write(uh, update, "iu_other", int64(1), "changed")
	fixture.RunStep(t, p.Ctx, uh, b.Recorder, fixture.Select(update, []string{"actor"}, []string{"TEXT"}, nil, int64(999), nil))
	run(fixture.Exec("RESET ROLE", 0))
	for _, q := range []string{
		"ALTER FUNCTION iu_ext.insert_username() OWNER TO iu_owner",
		"ALTER FUNCTION iu_ext.insert_username() SECURITY DEFINER",
		"ALTER FUNCTION iu_ext.insert_username() SET search_path=pg_catalog",
		"ALTER FUNCTION iu_ext.insert_username() RENAME TO iu_real",
		"CREATE SCHEMA iu_moved",
		"ALTER EXTENSION insert_username SET SCHEMA iu_moved",
		"GRANT USAGE ON SCHEMA iu_moved TO iu_owner,iu_writer,iu_other",
		"CREATE FUNCTION iu_ext.insert_username() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.actor := 'decoy'; RETURN NEW; END$$",
		"SET ROLE iu_writer",
	} { run(fixture.Exec(q, 0)) }
	write(ih, insert, "iu_owner", int64(3), nil, nil)
	caller := fixture.Select("SELECT current_user,current_setting('search_path') AS path", []string{"current_user", "path"}, []string{"NAME", "TEXT"}, [][]any{{"iu_writer", "public"}})
	run(caller)
	inventory := fixture.Select("SELECT p.prosecdef,p.proconfig,r.rolname,t.tgfoid=p.oid AS bound FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_trigger t ON t.tgfoid=p.oid WHERE n.nspname='iu_moved' AND p.proname='iu_real' AND t.tgname='iu_stamp'", []string{"prosecdef", "proconfig", "rolname", "bound"}, []string{"BOOL", "_TEXT", "NAME", "BOOL"}, [][]any{{true, "{search_path=pg_catalog}", "iu_owner", true}})
	insertUsernameBoth(t, p, b, c, inventory)
	saved := [][]any{{int64(1), "iu_other", "changed"}, {int64(2), "iu_writer", "two"}, {int64(3), "iu_owner", nil}}
	read := insertUsernameRows("SELECT id,actor,note FROM iu_data ORDER BY id", saved)
	run(read)
	run(fixture.Exec("RESET ROLE", 0))
	for _, q := range []string{"CREATE TABLE iu_control_bad(id integer,actor text)", "GRANT INSERT,SELECT ON iu_control_bad TO iu_writer", "CREATE TRIGGER iu_control_bad_stamp BEFORE INSERT ON iu_control_bad FOR EACH ROW EXECUTE FUNCTION iu_moved.iu_real('missing')", "SET ROLE iu_writer"} { run(fixture.Exec(q, 0)) }
	insertUsernameBoth(t, p, b, c, insertUsernameError("INSERT INTO iu_control_bad VALUES(1,NULL),(2,NULL)", "09000", `"iu_control_bad" has no attribute "missing"`))
	run(fixture.Select("SELECT id FROM iu_control_bad ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
	run(caller)
	write(uh, update, "iu_owner", int64(3), nil)
	run(fixture.Exec("RESET ROLE", 0))
	if b.PG { return }

	old, err := c.QueryContext(p.Ctx, read.Query)
	if err != nil { t.Fatal(err) }
	defer old.Close()
	cols, err := old.ColumnTypes()
	if err != nil { t.Fatal(err) }
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	for round := range 2 {
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		if round == 0 {
			for _, row := range saved {
				if !old.Next() { t.Fatal("retained row missing", old.Err()) }
				var id int64; var actor string; var note sql.NullString
				if err := old.Scan(&id, &actor, &note); err != nil { t.Fatal(err) }
				if id != row[0] || actor != row[1] || note.Valid != (row[2] != nil) || (note.Valid && note.String != row[2]) { t.Fatal("retained row changed", id, actor, note, row) }
			}
			if old.Next() || old.Err() != nil || len(cols) != 3 || cols[0].Name() != "id" || cols[0].DatabaseTypeName() != "INT4" || cols[1].Name() != "actor" || cols[1].DatabaseTypeName() != "TEXT" || cols[2].Name() != "note" || cols[2].DatabaseTypeName() != "TEXT" { t.Fatal("retained metadata/tail", old.Err()) }
			old.Close()
		}
		run(read)
		insertUsernameBoth(t, p, b, c, inventory)
		run(fixture.Exec("SET ROLE iu_writer", 0))
		write(ih, insert, "iu_owner", int64(4), "forged", nil)
		run(fixture.Prepared(fixture.Exec("INSERT INTO iu_data VALUES(5,NULL,'fresh')", 1)))
		run(fixture.Exec("INSERT INTO iu_data VALUES(6,NULL,'direct')", 1))
		run(fixture.Select("SELECT actor FROM iu_data WHERE id>=4 ORDER BY id", []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_owner"}, {"iu_owner"}, {"iu_owner"}}))
		run(caller)
		run(fixture.Exec("RESET ROLE", 0))
		for _, q := range []string{"ALTER FUNCTION iu_moved.iu_real() SECURITY INVOKER", "SET ROLE iu_writer"} { run(fixture.Exec(q, 0)) }
		write(uh, update, "iu_writer", int64(4), "invoker")
		run(caller)
		run(fixture.Exec("RESET ROLE", 0))
	}
	other, target, record := pgcryptoControlDestination(t, p, "iu_destination")
	// Roles are destination authority, not part of the database dump.
	for _, role := range []string{"iu_owner", "iu_writer", "iu_other"} {
		fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("CREATE ROLE "+role, 0))
	}
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil { t.Fatal(err) }
	for _, s := range []fixture.Step{read, fixture.Prepared(read), inventory, fixture.Prepared(inventory), fixture.Exec("SET ROLE iu_writer", 0), fixture.Exec("INSERT INTO iu_data VALUES(8,NULL,'destination')", 1), fixture.Select("SELECT actor FROM iu_data WHERE id=8", []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_owner"}}), fixture.Exec("RESET ROLE", 0)} {
		fixture.RunStep(t, p.Ctx, target, record, s)
	}
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil { t.Fatal("invalid restore accepted") }
	if fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("failed restore changed provider/data") }
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("canceled restore changed provider/data", err) }
	run(fixture.Exec("SET ROLE iu_other", 0))
	write(ih, insert, "iu_other", int64(7), nil, "after failure")
}

func TestReferenceInsertUsernameControls(t *testing.T) { insertUsernameCase(t, insertUsernameControls) }
