package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestSequenceOptionsOwnershipAndPhysicalLifetime(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a := sequenceConn(t, db)
	b := sequenceConn(t, db)
	for _, q := range []string{"CREATE SEQUENCE small AS SMALLINT MAXVALUE 32767", "ALTER SEQUENCE small AS INTEGER", "CREATE SEQUENCE explicit AS SMALLINT MAXVALUE 10", "ALTER SEQUENCE explicit AS BIGINT", "CREATE SEQUENCE extreme MINVALUE -9223372036854775808 MAXVALUE 9223372036854775807 START -9223372036854775808 INCREMENT 9223372036854775807", "CREATE SEQUENCE down INCREMENT -2 MINVALUE -5 MAXVALUE -1 START -1 CYCLE"} {
		sequenceExec(t, a, q)
	}
	sequenceValue(t, a, "SELECT setval('small',2147483647,false)", 2147483647)
	sequenceValue(t, a, "SELECT nextval('small')", 2147483647)
	sequenceValue(t, a, "SELECT nextval('extreme')", -9223372036854775808)
	sequenceValue(t, a, "SELECT nextval('extreme')", -1)
	sequenceValue(t, a, "SELECT nextval('extreme')", 9223372036854775806)
	for _, v := range []int64{-1, -3, -5, -1} {
		sequenceValue(t, a, "SELECT nextval('down')", v)
	}
	sequenceValue(t, a, "SELECT setval('explicit',10,false)", 10)
	sequenceValue(t, a, "SELECT nextval('explicit')", 10)
	_, boundErr := a.ExecContext(ctx, "SELECT nextval('explicit')")
	typeCoreDiagnostic(t, boundErr, stuntgres.Error{Code: "2200H", Message: `nextval: reached maximum value of sequence "explicit" (10)`})
	sequenceExec(t, a, "CREATE TABLE target(v INT)")
	sequenceExec(t, a, "CREATE SEQUENCE detached OWNED BY target.v")
	sequenceExec(t, a, "ALTER SEQUENCE detached OWNED BY NONE")
	sequenceExec(t, a, "DROP TABLE target RESTRICT")
	sequenceValue(t, a, "SELECT nextval('detached')", 1)
	sequenceExec(t, a, "CREATE SEQUENCE grouped")
	_, err := a.ExecContext(ctx, "DROP SEQUENCE grouped,missing")
	if err == nil {
		t.Fatal("grouped drop accepted")
	}
	sequenceValue(t, a, "SELECT nextval('grouped')", 1)
	sequenceExec(t, a, "CREATE SCHEMA seq_schema")
	sequenceExec(t, a, "CREATE SEQUENCE seq_schema.s")
	_, err = a.ExecContext(ctx, "DROP SCHEMA seq_schema RESTRICT")
	if err == nil {
		t.Fatal("schema restrict ignored sequence")
	}
	sequenceExec(t, a, "DROP SCHEMA seq_schema CASCADE")
	sequenceExec(t, a, "BEGIN")
	sequenceExec(t, a, "CREATE TEMP SEQUENCE rolled_temp")
	sequenceExec(t, a, "ROLLBACK")
	_, err = a.ExecContext(ctx, "SELECT nextval('pg_temp.rolled_temp')")
	if err == nil {
		t.Fatal("temporary creation survived rollback")
	}
	sequenceExec(t, a, "CREATE SEQUENCE shadow START 40")
	sequenceExec(t, a, "CREATE TEMP SEQUENCE shadow START 7 CACHE 3")
	sequenceValue(t, a, "SELECT nextval('shadow')", 7)
	sequenceValue(t, b, "SELECT nextval('shadow')", 40)
	sequenceExec(t, a, "BEGIN")
	sequenceExec(t, a, "DROP SEQUENCE pg_temp.shadow")
	sequenceExec(t, a, "ROLLBACK")
	sequenceValue(t, a, "SELECT nextval('shadow')", 8)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	db.SetMaxIdleConns(0)
	fresh := sequenceConn(t, db)
	sequenceValue(t, fresh, "SELECT nextval('shadow')", 41)
	var n int64
	err = fresh.QueryRowContext(ctx, "SELECT currval('shadow')").Scan(&n)
	if err != nil || n != 41 {
		t.Fatal(n, err)
	}
	sequenceExec(t, b, "CREATE SEQUENCE cached_exit CACHE 10")
	sequenceValue(t, b, "SELECT nextval('cached_exit')", 1)
	b.Close()
	sequenceValue(t, fresh, "SELECT nextval('cached_exit')", 11)
}

func TestSequenceEngineIsolationAndBorrowerMemory(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	c := sequenceConn(t, db)
	sequenceExec(t, c, "CREATE SEQUENCE s")
	sequenceValue(t, c, "SELECT nextval('s')", 1)
	c.Close()
	borrowed := sequenceConn(t, db)
	sequenceValue(t, borrowed, "SELECT currval('s')", 1)
	sequenceValue(t, borrowed, "SELECT lastval()", 1)
	borrowed.Close()
	shared := openLocal(t, d, t.Name())
	other := sequenceConn(t, shared)
	sequenceValue(t, other, "SELECT nextval('s')", 2)
	for _, isolated := range []*sql.DB{openLocal(t, d, t.Name()+"-different"), openLocal(t, stuntgres.NewDriver(), t.Name())} {
		fresh := sequenceConn(t, isolated)
		_, err := fresh.ExecContext(ctx, "SELECT nextval('s')")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
		sequenceExec(t, fresh, "CREATE SEQUENCE s")
		sequenceValue(t, fresh, "SELECT nextval('s')", 1)
	}
}
