package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestSequenceControlsOptionsStateSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "sequence-controls")
	a := sequenceConn(t, db)
	b := sequenceConn(t, db)
	for _, q := range []string{"CREATE SCHEMA other", "CREATE TABLE other.t(a INT,b INT,c INT)", "CREATE SEQUENCE other.s AS SMALLINT INCREMENT -2 MINVALUE -10 MAXVALUE -2 START -4 CACHE 3 CYCLE OWNED BY other.t.c", "CREATE UNLOGGED SEQUENCE uncalled START 7", "COMMENT ON SEQUENCE other.s IS 'generator metadata'"} {
		sequenceExec(t, a, q)
	}
	sequenceValue(t, a, "SELECT nextval('other.s')", -4)
	held, err := a.QueryContext(ctx, "SELECT * FROM other.s")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	types, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 3 || types[0].DatabaseTypeName() != "INT8" || types[2].DatabaseTypeName() != "BOOL" {
		t.Fatal(types)
	}
	var first bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &first); err != nil {
		t.Fatal(err)
	}
	sequenceExec(t, a, "CREATE TEMP SEQUENCE temporary START 50 CACHE 3")
	sequenceValue(t, a, "SELECT nextval('temporary')", 50)
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	sequenceValue(t, a, "SELECT nextval('temporary')", 51)
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(first.Bytes())); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("dump mismatch\n%s\n%s", first.Bytes(), second.Bytes())
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var last, log int64
	var called bool
	if err = held.Scan(&last, &log, &called); err != nil || last != -8 || log != 0 || !called {
		t.Fatal(last, log, called, err)
	}
	if held.Next() {
		t.Fatal("extra sequence row")
	}
	sequenceValue(t, b, "SELECT nextval('other.s')", -10)
	sequenceValue(t, b, "SELECT nextval('uncalled')", 7)
	sequenceValue(t, a, "SELECT nextval('temporary')", 52)
	_, err = a.ExecContext(ctx, "SELECT currval('other.s')")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "55000", Message: `currval of sequence "s" is not yet defined in this session`})
	// Invalid late content must not replace persistent objects or invalidate local memories.
	for _, stream := range []string{"CREATE SEQUENCE replacement; CREATE SEQUENCE invalid CACHE 0;", "CREATE SEQUENCE replacement; SELECT setval('replacement',0,true);", "CREATE SEQUENCE replacement; SELECT setval('replacement',1);", "CREATE SEQUENCE replacement; SELECT nextval('replacement');"} {
		if err = stuntgres.Restore(ctx, db, bytes.NewBufferString(stream)); err == nil {
			t.Fatal("bad restore accepted", stream)
		}
		sequenceValue(t, b, "SELECT currval('uncalled')", 7)
	}
	sequenceExec(t, b, "DROP TABLE other.t RESTRICT")
	_, err = b.ExecContext(ctx, "SELECT nextval('other.s')")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "other.s" does not exist`, Position: "16"})
}

func TestSequencePreparedIdentityStrictnessAndEffects(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "sequence-identity")
	a := sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE s")
	literal, err := a.PrepareContext(ctx, "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer literal.Close()
	parameter, err := a.PrepareContext(ctx, "SELECT nextval($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer parameter.Close()
	sequenceExec(t, a, "CREATE SCHEMA moved")
	sequenceExec(t, a, "ALTER SEQUENCE s SET SCHEMA moved")
	sequenceExec(t, a, "ALTER SEQUENCE moved.s RENAME TO renamed")
	var n int64
	err = literal.QueryRowContext(ctx).Scan(&n)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
	sequenceExec(t, a, "DROP SEQUENCE moved.renamed")
	sequenceExec(t, a, "CREATE SEQUENCE s START 20")
	if err = literal.QueryRowContext(ctx).Scan(&n); err != nil || n != 20 {
		t.Fatal("invalidated prepared source must adopt newly resolved identity", n, err)
	}
	if err = parameter.QueryRowContext(ctx, "s").Scan(&n); err != nil || n != 21 {
		t.Fatal(n, err)
	}
	rows, err := a.QueryContext(ctx, "SELECT nextval(NULL),setval('s',NULL),setval('s',1,NULL)")
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "INT8" {
			t.Fatal(typ.DatabaseTypeName())
		}
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var nulls [3]sql.NullInt64
	if err = rows.Scan(&nulls[0], &nulls[1], &nulls[2]); err != nil {
		t.Fatal(err)
	}
	if nulls != ([3]sql.NullInt64{}) {
		t.Fatal(nulls)
	}
	rows.Close()
	sequenceValue(t, a, "SELECT currval('s')", 21)
	sequenceExec(t, a, "CREATE TABLE t(v BIGINT NOT NULL,w BIGINT)")
	_, err = a.ExecContext(ctx, "INSERT INTO t VALUES(nextval('s'),currval('unused'))")
	if err == nil {
		t.Fatal("missing literal accepted")
	}
	sequenceValue(t, a, "SELECT currval('s')", 21)
	sequenceExec(t, a, "CREATE SEQUENCE unused")
	_, err = a.ExecContext(ctx, "INSERT INTO t VALUES(nextval('s'),currval('unused'))")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "55000", Message: `currval of sequence "unused" is not yet defined in this session`})
	sequenceValue(t, a, "SELECT currval('s')", 22)
	rows, err = a.QueryContext(ctx, "SELECT * FROM t")
	queryCoreRows(t, rows, err, []string{"v", "w"}, []string{"INT8", "INT8"}, [][]any{}, false)
	sequenceValue(t, a, "SELECT nextval('s')", 23)
}

func TestSequenceControlFailuresPreserveStateAndAbortedTransaction(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a := sequenceConn(t, db)
	b := sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE sentinel")
	sequenceValue(t, a, "SELECT nextval('sentinel')", 1)
	held, err := a.PrepareContext(ctx, "SELECT nextval('sentinel')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	failure := errors.New("sequence control I/O failure")
	if err = stuntgres.Dump(ctx, db, failingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, failingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, db, bytes.NewBufferString("CREATE SEQUENCE replacement;")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, stream := range []string{"CREATE SEQUENCE replacement; SELECT setval('replacement',$1,true);", "CREATE SEQUENCE replacement; SELECT setval('replacement',1,true,false);", "SET SESSION AUTHORIZATION missing_owner; CREATE SEQUENCE replacement;"} {
		if err = stuntgres.Restore(ctx, db, bytes.NewBufferString(stream)); err == nil {
			t.Fatal("invalid stream accepted", stream)
		}
	}
	var v int64
	if err = held.QueryRowContext(ctx).Scan(&v); err != nil || v != 2 {
		t.Fatal("failed replacement invalidated prepared state", v, err)
	}
	sequenceExec(t, a, "BEGIN")
	_, err = a.ExecContext(ctx, "SELECT currval('missing')")
	if err == nil {
		t.Fatal("missing relation accepted")
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE SEQUENCE bad CACHE 0;")); err == nil {
		t.Fatal("bad restore accepted")
	}
	_, err = a.ExecContext(ctx, "SELECT nextval('sentinel')")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	sequenceExec(t, a, "ROLLBACK")
	sequenceValue(t, b, "SELECT nextval('sentinel')", 3)
}

func TestSequenceTemporaryPreparedIdentitySurvivesPersistentControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a := sequenceConn(t, db)
	b := sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE persistent")
	sequenceExec(t, a, "CREATE TEMP SEQUENCE temporary START 50 CACHE 7")
	next, err := a.PrepareContext(ctx, "SELECT nextval('pg_temp.temporary')")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	current, err := a.PrepareContext(ctx, "SELECT currval('pg_temp.temporary')")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	var v int64
	if err = next.QueryRowContext(ctx).Scan(&v); err != nil || v != 50 {
		t.Fatal(v, err)
	}
	for i, replace := range []func() error{func() error { return stuntgres.Reset(ctx, db) }, func() error { return stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())) }} {
		if err = replace(); err != nil {
			t.Fatal(err)
		}
		if err = current.QueryRowContext(ctx).Scan(&v); err != nil || v != int64(50+i) {
			t.Fatal("retained temp currval", v, err)
		}
		sequenceValue(t, a, "SELECT lastval()", int64(50+i))
		if err = next.QueryRowContext(ctx).Scan(&v); err != nil || v != int64(51+i) {
			t.Fatal("retained temp nextval", v, err)
		}
		if _, err = b.ExecContext(ctx, "SELECT nextval('pg_temp.temporary')"); err == nil {
			t.Fatal("peer adopted temporary identity")
		}
	}
	// A genuine namespace-environment change must not erase original native
	// replacement provenance when reanalyzing an exact retained temporary OID.
	sequenceExec(t, a, "SET search_path TO pg_catalog,public")
	if err = next.QueryRowContext(ctx).Scan(&v); err != nil || v != 53 {
		t.Fatal("temporary group refresh", v, err)
	}
	sequenceExec(t, a, "DROP SEQUENCE pg_temp.temporary")
	sequenceExec(t, a, "CREATE TEMP SEQUENCE temporary START 100")
	if err = next.QueryRowContext(ctx).Scan(&v); err == nil {
		t.Fatal("retired temporary literal adopted recreated identity")
	}
	sequenceValue(t, a, "SELECT nextval('temporary')", 100)
}
