package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWCursorLifecycle(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE SEQUENCE fdw_cursor_draw START 1", 0))
		batch := func(q string, values [][]any) fixture.Step {
			return fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, values)
		}
		sequence := func(value int64, called bool) {
			run(fixture.Select("SELECT last_value,is_called FROM fdw_cursor_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}}))
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_frontier NO SCROLL CURSOR FOR SELECT id,label,nextval('fdw_cursor_draw') AS draw FROM fdw_data", 0))
		sequence(1, false)
		run(fixture.Select("FETCH 1 FROM fdw_frontier", []string{"id", "label", "draw"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "one", int64(1)}}))
		sequence(1, true)
		run(fixture.Select("FETCH 1 FROM fdw_frontier", []string{"id", "label", "draw"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(2), nil, int64(2)}}))
		sequence(2, true)
		run(fixture.Exec("CLOSE fdw_frontier", 0))
		sequence(2, true)
		run(fixture.Exec("COMMIT", 0))

		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_eof NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		run(batch("FETCH ALL FROM fdw_eof", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
		run(batch("FETCH 1 FROM fdw_eof", nil))
		run(fixture.Exec("CLOSE fdw_eof", 0))
		run(fixture.Exec("COMMIT", 0))

		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_scroll SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		run(batch("FETCH 2 FROM fdw_scroll", [][]any{{int64(1), "one"}, {int64(2), nil}}))
		run(batch("FETCH BACKWARD 1 FROM fdw_scroll", [][]any{{int64(1), "one"}}))
		run(batch("FETCH FORWARD ALL FROM fdw_scroll", [][]any{{int64(2), nil}, {int64(3), "three"}}))
		run(fixture.Exec("CLOSE fdw_scroll", 0))
		run(fixture.Exec("ROLLBACK", 0))

		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_hold NO SCROLL CURSOR WITH HOLD FOR SELECT id,label FROM fdw_data", 0))
		run(batch("FETCH 1 FROM fdw_hold", [][]any{{int64(1), "one"}}))
		run(fixture.Exec("COMMIT", 0))
		run(batch("FETCH ALL FROM fdw_hold", [][]any{{int64(2), nil}, {int64(3), "three"}}))
		run(fixture.Exec("CLOSE fdw_hold", 0))

		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_parent NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		run(batch("FETCH 1 FROM fdw_parent", [][]any{{int64(1), "one"}}))
		run(fixture.Exec("SAVEPOINT fdw_child_birth", 0))
		run(fixture.Exec("DECLARE fdw_child NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		run(batch("FETCH 1 FROM fdw_child", [][]any{{int64(1), "one"}}))
		run(fixture.Exec("ROLLBACK TO fdw_child_birth", 0))
		run(batch("FETCH 1 FROM fdw_parent", [][]any{{int64(2), nil}}))
		run(fixture.Exec("CLOSE ALL", 0))
		run(fixture.Exec("CLOSE ALL", 0))
		run(fixture.Exec("ROLLBACK", 0))

		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DECLARE fdw_terminal NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
			run(batch("FETCH 1 FROM fdw_terminal", [][]any{{int64(1), "one"}}))
			run(fixture.Exec(end, 0))
			run(fixture.Select("SELECT count(*) FROM pg_cursors WHERE name='fdw_terminal'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}

		// The volatile denominator is deliberately local: postgres_fdw cannot
		// push the division into its larger remote fetch batch.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_bad NO SCROLL CURSOR FOR SELECT 10/(id-2+0*nextval('fdw_cursor_draw')::integer) AS n FROM fdw_data", 0))
		run(fixture.Exec("SAVEPOINT fdw_before_bad_fetch", 0))
		run(fixture.Select("FETCH 1 FROM fdw_bad", []string{"n"}, []string{"INT4"}, [][]any{{int64(-10)}}))
		run(fixture.ErrorDiagnostic("FETCH 1 FROM fdw_bad", fdwDiagnostic("22012", "division by zero")))
		run(fixture.Exec("ROLLBACK TO fdw_before_bad_fetch", 0))
		run(fixture.ErrorDiagnostic("FETCH 1 FROM fdw_bad", fdwDiagnostic("55000", `portal "fdw_bad" cannot be run`)))
		run(fixture.Exec("ROLLBACK TO fdw_before_bad_fetch", 0))
		run(fixture.Exec("CLOSE fdw_bad", 0))
		run(fixture.Exec("ROLLBACK", 0))
		fdwCursorNative(t, p, b, c, reader, run)
	})
}
