package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func deferredSavedNamesSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE ds.names(id integer,CONSTRAINT check_name CHECK(id>0))",
		"CREATE TABLE ds.names_log(id integer,label text)",
		"CREATE FUNCTION ds.names_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds.names_log VALUES(NEW.id,TG_NAME); RETURN NULL; END$$",
		"CREATE CONSTRAINT TRIGGER a AFTER INSERT ON ds.names DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ds.names_event()",
		"ALTER TRIGGER a ON ds.names RENAME TO temporary",
		"CREATE CONSTRAINT TRIGGER b AFTER INSERT ON ds.names DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ds.names_event()",
		"ALTER TRIGGER b ON ds.names RENAME TO a",
		"ALTER TRIGGER temporary ON ds.names RENAME TO b",
		"CREATE CONSTRAINT TRIGGER c AFTER INSERT ON ds.names DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ds.names_event()",
		"ALTER TRIGGER c ON ds.names RENAME TO check_name")
}

func deferredSavedNames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Select("SELECT t.tgname,c.conname,c.contype FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid='ds.names'::regclass ORDER BY t.tgname", []string{"tgname", "conname", "contype"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"a", "b", "t"}, {"b", "a", "t"}, {"check_name", "c", "t"}}))
	run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='ds.names'::regclass AND conname='check_name'", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"check_name", "c"}}))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO ds.names VALUES($1)", 1, 1)))
	run(deferredCount("ds.names_log", 0))
	run(fixture.Prepared(fixture.Exec("SET CONSTRAINTS ds.a IMMEDIATE", 0)))
	run(fixture.Select("SELECT id,label FROM ds.names_log ORDER BY label", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "b"}}))
	run(fixture.Exec("SET CONSTRAINTS ds.b IMMEDIATE", 0))
	run(fixture.Select("SELECT id,label FROM ds.names_log ORDER BY label", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}}))
	run(fixture.Exec("SET CONSTRAINTS ds.c IMMEDIATE", 0))
	run(fixture.Select("SELECT id,label FROM ds.names_log ORDER BY label", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(1), "check_name"}}))
	run(fixture.Exec("ROLLBACK", 0))
	run(deferredCount("ds.names_log", 0))
	run(deferredCount("ds.names", 0))
}
