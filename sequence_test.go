package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

// These public behavioral witnesses intentionally precede production implementation.
func sequenceConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func sequenceExec(t *testing.T, c *sql.Conn, q string) {
	t.Helper()
	r, err := c.ExecContext(context.Background(), q)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatalf("%s affected=%d err=%v", q, n, err)
	}
}
func sequenceValue(t *testing.T, c *sql.Conn, q string, want int64) {
	t.Helper()
	var got int64
	if err := c.QueryRowContext(context.Background(), q).Scan(&got); err != nil || got != want {
		t.Fatalf("%s got=%d want=%d err=%v", q, got, want, err)
	}
}
func TestSequenceDefaultsPrepared(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := sequenceConn(t, db)
	sequenceExec(t, c, "CREATE SEQUENCE s")
	stmt, err := c.PrepareContext(context.Background(), "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, want := range []int64{1, 2} {
		rows, err := stmt.QueryContext(context.Background())
		queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{{want}}, false)
	}
	sequenceValue(t, c, "SELECT currval('s')", 2)
	sequenceValue(t, c, "SELECT lastval()", 2)
	rows, err := c.QueryContext(context.Background(), "SELECT nextval(NULL),currval(NULL),setval(NULL,42),setval('s',NULL),setval('s',42,NULL)")
	queryCoreRows(t, rows, err, []string{"nextval", "currval", "setval", "setval", "setval"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{nil, nil, nil, nil, nil}}, false)
	sequenceValue(t, c, "SELECT nextval('s')", 3)
}
func TestSequenceSessionsSetvalRollback(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, b := sequenceConn(t, db), sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE s")
	sequenceExec(t, a, "CREATE SEQUENCE t")
	for _, tc := range []struct{ q, msg string }{{"SELECT currval('s')", `currval of sequence "s" is not yet defined in this session`}, {"SELECT lastval()", "lastval is not yet defined in this session"}} {
		var v int64
		err := a.QueryRowContext(context.Background(), tc.q).Scan(&v)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "55000", Message: tc.msg})
	}
	sequenceValue(t, a, "SELECT nextval('s')", 1)
	sequenceValue(t, b, "SELECT nextval('s')", 2)
	sequenceValue(t, a, "SELECT currval('s')", 1)
	sequenceValue(t, a, "SELECT setval('s',42)", 42)
	sequenceValue(t, a, "SELECT currval('s')", 42)
	sequenceValue(t, a, "SELECT nextval('s')", 43)
	sequenceValue(t, a, "SELECT setval('s',42,false)", 42)
	sequenceValue(t, a, "SELECT currval('s')", 43)
	sequenceValue(t, a, "SELECT nextval('s')", 42)
	sequenceValue(t, a, "SELECT setval('t',80,true)", 80)
	sequenceValue(t, a, "SELECT lastval()", 42)
	sequenceExec(t, a, "BEGIN")
	sequenceValue(t, a, "SELECT nextval('s')", 43)
	sequenceExec(t, a, "ROLLBACK")
	sequenceValue(t, b, "SELECT nextval('s')", 44)
	sequenceExec(t, a, "BEGIN")
	sequenceValue(t, a, "SELECT setval('s',90,false)", 90)
	sequenceExec(t, a, "ROLLBACK")
	sequenceValue(t, b, "SELECT nextval('s')", 90)
}
func TestSequenceCacheBoundsRow(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, b := sequenceConn(t, db), sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE s CACHE 10")
	sequenceValue(t, a, "SELECT nextval('s')", 1)
	sequenceValue(t, b, "SELECT nextval('s')", 11)
	sequenceValue(t, a, "SELECT nextval('s')", 2)
	rows, err := a.QueryContext(context.Background(), "SELECT * FROM s")
	queryCoreRows(t, rows, err, []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(20), int64(0), true}}, false)
	sequenceValue(t, b, "SELECT setval('s',50)", 50)
	sequenceValue(t, a, "SELECT nextval('s')", 3)
	sequenceValue(t, b, "SELECT nextval('s')", 51)
	sequenceExec(t, a, "CREATE SEQUENCE bounded AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 1 NO CYCLE")
	sequenceValue(t, a, "SELECT nextval('bounded')", 2)
	sequenceValue(t, a, "SELECT nextval('bounded')", 4)
	var v int64
	err = a.QueryRowContext(context.Background(), "SELECT nextval('bounded')").Scan(&v)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "2200H", Message: `nextval: reached maximum value of sequence "bounded" (5)`})
	sequenceExec(t, a, "ALTER SEQUENCE bounded CYCLE")
	sequenceValue(t, a, "SELECT nextval('bounded')", 2)
}
func TestSequenceTransactionalRestartDDL(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, b := sequenceConn(t, db), sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE s")
	sequenceValue(t, a, "SELECT nextval('s')", 1)
	sequenceExec(t, a, "BEGIN")
	sequenceExec(t, a, "ALTER SEQUENCE s RESTART WITH 100")
	sequenceValue(t, a, "SELECT nextval('s')", 100)
	sequenceExec(t, a, "ROLLBACK")
	sequenceValue(t, a, "SELECT currval('s')", 100)
	sequenceValue(t, b, "SELECT nextval('s')", 2)
	sequenceExec(t, a, "BEGIN")
	sequenceExec(t, a, "CREATE SEQUENCE rolled_back")
	sequenceExec(t, a, "ROLLBACK")
	var v int64
	err := b.QueryRowContext(context.Background(), "SELECT nextval('rolled_back')").Scan(&v)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "rolled_back" does not exist`, Position: "16"})
	sequenceExec(t, a, "BEGIN")
	sequenceExec(t, a, "DROP SEQUENCE s")
	sequenceExec(t, a, "ROLLBACK")
	sequenceValue(t, b, "SELECT nextval('s')", 3)
	sequenceExec(t, a, "ALTER SEQUENCE s START WITH 7")
	sequenceValue(t, a, "SELECT nextval('s')", 4)
	sequenceExec(t, a, "ALTER SEQUENCE s RESTART")
	sequenceValue(t, a, "SELECT nextval('s')", 7)
}
func TestSequenceOwnershipTemporaryForms(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, b := sequenceConn(t, db), sequenceConn(t, db)
	sequenceExec(t, a, "CREATE TABLE t(a INT,b INT,c INT)")
	sequenceExec(t, a, "CREATE SEQUENCE owned OWNED BY t.c")
	sequenceExec(t, a, "ALTER TABLE t DROP COLUMN b")
	sequenceExec(t, a, "ALTER TABLE t DROP COLUMN c RESTRICT")
	var v int64
	err := a.QueryRowContext(context.Background(), "SELECT nextval('owned')").Scan(&v)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "owned" does not exist`, Position: "16"})
	sequenceExec(t, a, "CREATE UNLOGGED SEQUENCE s START 40")
	sequenceExec(t, a, "ALTER SEQUENCE s SET LOGGED")
	sequenceExec(t, a, "CREATE TEMPORARY SEQUENCE s START 7")
	sequenceValue(t, a, "SELECT nextval('s')", 7)
	sequenceValue(t, b, "SELECT nextval('s')", 40)
	sequenceValue(t, a, "SELECT nextval('pg_temp.s')", 8)
	sequenceExec(t, a, "ALTER SEQUENCE pg_temp.s RENAME TO local_s")
	sequenceValue(t, a, "SELECT nextval('local_s')", 9)
}

func TestSequenceHugeCacheArithmetic(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a := sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE huge CACHE 9223372036854775807")
	sequenceValue(t, a, "SELECT nextval('huge')", 1)
	sequenceValue(t, a, "SELECT last_value FROM huge", 9223372036854775807)
	sequenceValue(t, a, "SELECT nextval('huge')", 2)
}
