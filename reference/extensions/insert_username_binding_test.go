package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func insertUsernameBinding(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	insertUsernameSetup(t, p, b, c)
	run := insertUsernameRun(t, p, b, c)
	q := "INSERT INTO iu_data VALUES($1,NULL,NULL) RETURNING actor"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	var oid string
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_proc WHERE proname='insert_username'").Scan(&oid); err != nil { t.Fatal(err) }
	for _, q := range []string{
		"SET search_path=iu_ext,public",
		"CREATE TABLE public.iu_unqualified(id integer,actor text)",
		"CREATE TRIGGER iu_unqualified_stamp BEFORE INSERT ON public.iu_unqualified FOR EACH ROW EXECUTE FUNCTION insert_username('actor')",
		"SET search_path=public",
		"ALTER FUNCTION iu_ext.insert_username() RENAME TO iu_real",
		"CREATE SCHEMA iu_moved",
		"ALTER EXTENSION insert_username SET SCHEMA iu_moved",
		"GRANT USAGE ON SCHEMA iu_moved TO iu_writer,iu_owner,iu_other",
		"CREATE FUNCTION iu_ext.insert_username() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.actor := 'decoy'; RETURN NEW; END$$",
		"CREATE FUNCTION public.insert_username() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.actor := 'decoy'; RETURN NEW; END$$",
		"SET ROLE iu_writer",
	} { run(fixture.Exec(q, 0)) }
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_writer"}}, int64(1)))
	run(fixture.Prepared(fixture.Exec("INSERT INTO iu_data VALUES(2,NULL,'fresh')", 1)))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("INSERT INTO iu_unqualified VALUES(1,NULL)", 1))
	run(fixture.Select("SELECT actor=current_user::text AS authentic FROM iu_unqualified", []string{"authentic"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT p.oid::text AS oid,t.tgfoid=p.oid AS bound FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_trigger t ON t.tgfoid=p.oid WHERE n.nspname='iu_moved' AND p.proname='iu_real' AND t.tgname='iu_stamp'", []string{"oid", "bound"}, []string{"TEXT", "BOOL"}, [][]any{{oid, true}}))
	for _, q := range []string{"CREATE TABLE iu_decoy(id integer,actor text)", "CREATE TRIGGER iu_decoy_stamp BEFORE INSERT ON iu_decoy FOR EACH ROW EXECUTE FUNCTION public.insert_username('actor')"} { run(fixture.Exec(q, 0)) }
	run(fixture.Exec("INSERT INTO iu_decoy VALUES(1,NULL)", 1))
	run(fixture.Select("SELECT actor FROM iu_decoy", []string{"actor"}, []string{"TEXT"}, [][]any{{"decoy"}}))
	run(fixture.Exec("ALTER TABLE iu_data RENAME COLUMN actor TO renamed_actor", 0))
	insertUsernameBoth(t, p, b, c, insertUsernameError("INSERT INTO iu_data VALUES(3,NULL,NULL)", "09000", `"iu_data" has no attribute "actor"`))
	run(fixture.Exec("ALTER TABLE iu_data RENAME TO iu_renamed_table", 0))
	insertUsernameBoth(t, p, b, c, insertUsernameError("INSERT INTO iu_renamed_table VALUES(3,NULL,NULL)", "09000", `"iu_renamed_table" has no attribute "actor"`))
	for _, q := range []string{"DROP TRIGGER iu_stamp ON iu_renamed_table", "CREATE TRIGGER iu_stamp BEFORE INSERT OR UPDATE ON iu_renamed_table FOR EACH ROW EXECUTE FUNCTION iu_moved.iu_real('renamed_actor')", "SET ROLE iu_writer"} { run(fixture.Exec(q, 0)) }
	run(fixture.Exec("INSERT INTO iu_renamed_table VALUES(3,NULL,NULL)", 1))
	run(fixture.Select("SELECT renamed_actor FROM iu_renamed_table WHERE id=3", []string{"renamed_actor"}, []string{"TEXT"}, [][]any{{"iu_writer"}}))
	run(fixture.Exec("RESET ROLE", 0))
	for _, q := range []string{"CREATE TABLE iu_slots(id integer,actor text)", "CREATE TRIGGER iu_slots_stamp BEFORE INSERT ON iu_slots FOR EACH ROW EXECUTE FUNCTION iu_moved.iu_real('actor')", "ALTER TABLE iu_slots DROP COLUMN actor"} { run(fixture.Exec(q, 0)) }
	run(insertUsernameError("INSERT INTO iu_slots(id) VALUES(1)", "09000", `"iu_slots" has no attribute "actor"`))
	run(fixture.Exec("ALTER TABLE iu_slots ADD COLUMN actor integer", 0))
	run(insertUsernameError("INSERT INTO iu_slots(id) VALUES(1)", "09000", `attribute "actor" of "iu_slots" must be type TEXT`))
	run(fixture.Exec("ALTER TABLE iu_slots DROP COLUMN actor", 0))
	run(fixture.Exec("ALTER TABLE iu_slots ADD COLUMN actor text", 0))
	run(fixture.Exec("INSERT INTO iu_slots(id) VALUES(1)", 1))
	run(fixture.Select("SELECT actor=current_user::text AS authentic FROM iu_slots", []string{"authentic"}, []string{"BOOL"}, [][]any{{true}}))
}

func TestReferenceInsertUsernameBinding(t *testing.T) { insertUsernameCase(t, insertUsernameBinding) }
