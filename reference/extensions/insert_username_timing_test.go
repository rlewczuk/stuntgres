package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInsertUsernameTiming(t *testing.T) {
	for _, tc := range []struct { name, clause, command, message string }{
		{"delete_priority", "BEFORE DELETE ON iu_timing FOR EACH ROW", "DELETE FROM iu_timing", "insert_username: cannot process DELETE events"},
		{"after_row", "AFTER INSERT ON iu_timing FOR EACH ROW", "INSERT INTO iu_timing VALUES(2,'forged')", "insert_username: must be fired before event"},
		{"after_statement", "AFTER INSERT ON iu_timing FOR EACH STATEMENT", "INSERT INTO iu_timing VALUES(2,'forged')", "insert_username: must be fired for row"},
		{"before_statement", "BEFORE INSERT ON iu_timing FOR EACH STATEMENT", "INSERT INTO iu_timing VALUES(2,'forged')", "insert_username: must be fired for row"},
		{"truncate", "BEFORE TRUNCATE ON iu_timing FOR EACH STATEMENT", "TRUNCATE iu_timing", "insert_username: must be fired for row"},
		{"empty_statement", "AFTER UPDATE ON iu_timing FOR EACH STATEMENT", "UPDATE iu_timing SET actor=NULL WHERE id=999", "insert_username: must be fired for row"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := insertUsernameRun(t, p, b, c)
				for _, q := range []string{"CREATE EXTENSION insert_username", "CREATE TABLE iu_timing(id integer,actor text)"} { run(fixture.Exec(q, 0)) }
				run(fixture.Exec("INSERT INTO iu_timing VALUES(1,'original')", 1))
				run(fixture.Exec("CREATE TRIGGER iu_timing_stamp "+tc.clause+" EXECUTE FUNCTION insert_username()", 0))
				insertUsernameBoth(t, p, b, c, insertUsernameError(tc.command, "XX000", tc.message))
				run(fixture.Select("SELECT id,actor FROM iu_timing ORDER BY id", []string{"id", "actor"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "original"}}))
			})
		})
	}
	t.Run("instead_of", func(t *testing.T) {
		insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := insertUsernameRun(t, p, b, c)
			for _, q := range []string{"CREATE EXTENSION insert_username", "CREATE TABLE iu_base(id integer,actor text)", "CREATE VIEW iu_view AS SELECT id,actor FROM iu_base", "CREATE TRIGGER iu_view_stamp INSTEAD OF INSERT ON iu_view FOR EACH ROW EXECUTE FUNCTION insert_username()"} { run(fixture.Exec(q, 0)) }
			insertUsernameBoth(t, p, b, c, insertUsernameError("INSERT INTO iu_view VALUES(1,NULL)", "XX000", "insert_username: must be fired before event"))
			run(fixture.Select("SELECT id FROM iu_base ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
		})
	})
	t.Run("not_reached", func(t *testing.T) {
		insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := insertUsernameRun(t, p, b, c)
			for _, q := range []string{"CREATE EXTENSION insert_username", "CREATE TABLE iu_unreached(id integer,actor text)", "CREATE TRIGGER iu_before BEFORE UPDATE ON iu_unreached FOR EACH ROW EXECUTE FUNCTION insert_username()", "CREATE TRIGGER iu_after AFTER UPDATE ON iu_unreached FOR EACH ROW EXECUTE FUNCTION insert_username()", "CREATE TRIGGER iu_false BEFORE INSERT ON iu_unreached FOR EACH ROW WHEN(false) EXECUTE FUNCTION insert_username()"} { run(fixture.Exec(q, 0)) }
			insertUsernameBoth(t, p, b, c, fixture.Exec("UPDATE iu_unreached SET actor=NULL", 0))
			run(fixture.Exec("INSERT INTO iu_unreached VALUES(1,'untouched')", 1))
			run(fixture.Select("SELECT actor FROM iu_unreached", []string{"actor"}, []string{"TEXT"}, [][]any{{"untouched"}}))
		})
	})
}

func TestReferenceInsertUsernameAtomicity(t *testing.T) {
	insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := insertUsernameRun(t, p, b, c)
		for _, q := range []string{"CREATE EXTENSION insert_username", "CREATE TABLE iu_atomic(id integer,actor text)"} { run(fixture.Exec(q, 0)) }
		run(fixture.Exec("INSERT INTO iu_atomic VALUES(1,'one'),(2,'two')", 2))
		run(fixture.Exec("CREATE TRIGGER iu_atomic_stamp BEFORE INSERT OR UPDATE ON iu_atomic FOR EACH ROW EXECUTE FUNCTION insert_username('missing')", 0))
		baseline := fixture.Select("SELECT id,actor FROM iu_atomic ORDER BY id", []string{"id", "actor"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}})
		for _, q := range []string{"INSERT INTO iu_atomic VALUES(3,NULL),(4,NULL)", "UPDATE iu_atomic SET actor='changed'"} {
			insertUsernameBoth(t, p, b, c, insertUsernameError(q, "09000", `"iu_atomic" has no attribute "missing"`))
			run(baseline)
		}
		run(fixture.Exec("BEGIN", 0))
		run(insertUsernameError("UPDATE iu_atomic SET actor=NULL", "09000", `"iu_atomic" has no attribute "missing"`))
		run(fixture.ImplicitError("SELECT id FROM iu_atomic", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK", 0))
		run(baseline)
		run(fixture.Exec("DROP TRIGGER iu_atomic_stamp ON iu_atomic", 0))
		run(fixture.Exec("INSERT INTO iu_atomic VALUES(3,'recovered')", 1))
	})
}
