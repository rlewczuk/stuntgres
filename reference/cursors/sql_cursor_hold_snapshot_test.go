package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func holdSnapshotAndImplicit(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	sqlCursorSetup(t, p, b, c)
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	holdRun(t, p, b, c, "BEGIN")
	sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO cursor_rows VALUES(4,'preceding')", 1))
	holdRun(t, p, b, c, holdDeclare("h"))
	sqlCursorStep(t, p, b, peer, fixture.Exec("UPDATE cursor_rows SET t='peer' WHERE i=1", 1))
	sqlCursorStep(t, p, b, peer, fixture.Exec("INSERT INTO cursor_rows VALUES(5,'peer')", 1))
	sqlCursorStep(t, p, b, c, fixture.Exec("UPDATE cursor_rows SET t='later' WHERE i=2", 1))
	sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO cursor_rows VALUES(6,'later')", 1))
	holdRun(t, p, b, c, "COMMIT")
	holdBatch(t, p, b, c, "FETCH ALL FROM h", []any{int64(1), "one"}, []any{int64(2), nil}, []any{int64(3), "three"}, []any{int64(4), "preceding"})
	holdRun(t, p, b, c, "CLOSE h")
	holdRun(t, p, b, c, "CREATE SEQUENCE hold_implicit_draw START 10 CACHE 1")
	holdRun(t, p, b, c, "DECLARE implicit_h NO SCROLL CURSOR WITH HOLD FOR SELECT i,nextval('hold_implicit_draw') AS n FROM cursor_rows WHERE i<=3 ORDER BY i")
	sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM hold_implicit_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(12), true}}))
	sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM implicit_h", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}, {int64(3), int64(12)}}))
	sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value FROM hold_implicit_draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(12)}}))
}
