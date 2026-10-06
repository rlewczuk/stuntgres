package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestNotNullControlsCanonicalIdentityAndAtomicRestore(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE USER nn_owner")
	execSQL(t, db, "CREATE SCHEMA s AUTHORIZATION nn_owner")
	execSQL(t, db, "SET SESSION AUTHORIZATION nn_owner")
	execSQL(t, db, `CREATE TABLE s.t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`)
	text := "O'Brien\n\\tail"
	execSQL(t, db, "INSERT INTO s.t VALUES($1,$2,$3,$4)", 7, int64(9223372036854775807), text, true)
	execSQL(t, db, "INSERT INTO s.t VALUES(8,NULL,NULL,NULL)")
	execSQL(t, db, "CREATE TABLE s.generated(a INT NOT NULL)")
	execSQL(t, db, "CREATE TABLE s.ordered(a INT,b INT,CONSTRAINT b_nn NOT NULL b,CONSTRAINT a_nn NOT NULL a)")
	execSQL(t, db, "COMMENT ON TABLE s.t IS 'persistent'")
	execSQL(t, db, "COMMENT ON COLUMN s.t.a IS 'attribute'")
	execSQL(t, db, "RESET SESSION AUTHORIZATION")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`CONSTRAINT "n""é\\tail" NOT NULL`, `CONSTRAINT "generated_a_not_null" NOT NULL`, `CREATE SCHEMA "s" AUTHORIZATION "nn_owner";`} {
		if !strings.Contains(dump.String(), part) {
			t.Fatal(part, dump.String())
		}
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM s.t ORDER BY a", [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}})
	alterDropMetadata(t, db, true, "SELECT * FROM s.t", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, 2)
	tableCoreMetadata(t, db, true, "SELECT * FROM s.ordered", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, false})
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || dump.String() != redump.String() {
		t.Fatal(err, redump.String())
	}
	execSQL(t, db, "CREATE TABLE sentinel(v INT)")
	execSQL(t, db, "INSERT INTO sentinel VALUES(91)")
	for _, tc := range []struct {
		script string
		want   stuntgres.Error
	}{
		{"CREATE TABLE replacement(a INT CONSTRAINT nn NOT NULL);INSERT INTO replacement VALUES(NULL)", stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"}},
		{"CREATE TABLE replacement(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", stuntgres.Error{Code: "42710", Message: `constraint "nn" for relation "replacement" already exists`}},
		{"CREATE TABLE replacement(a INT,NOT NULL missing)", stuntgres.Error{Code: "42703", Message: `column "missing" of relation "replacement" does not exist`}},
		{"CREATE TABLE replacement(a INT NOT NULL,b INT CONSTRAINT replacement_a_not_null NOT NULL)", stuntgres.Error{Code: "23505"}},
	} {
		var before bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &before); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query("SELECT * FROM sentinel")
		if err != nil {
			t.Fatal(err)
		}
		err = stuntgres.Restore(ctx, db, strings.NewReader(tc.script))
		if tc.want.Code == "23505" {
			notNullNameCollisionDiagnostic(t, err, "replacement_a_not_null")
		} else {
			notNullDiagnostic(t, err, tc.want)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var n int64
		if err = rows.Scan(&n); err != nil || n != 91 {
			t.Fatal(n, err)
		}
		rows.Close()
		var after bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &after); err != nil || before.String() != after.String() {
			t.Fatal(err)
		}
		wantTuples(t, db, "SELECT * FROM sentinel", [][]any{{int64(91)}})
	}
	execSQL(t, db, "SET SESSION AUTHORIZATION nn_owner")
	tableCoreExec(t, db, true, `ALTER TABLE s.t DROP CONSTRAINT "n""é\\tail"`, 0)
	tableCoreExec(t, db, false, "ALTER TABLE s.generated DROP CONSTRAINT generated_a_not_null", 0)
	tableCoreExec(t, db, true, "ALTER TABLE s.ordered DROP CONSTRAINT a_nn", 0)
	tableCoreExec(t, db, false, "ALTER TABLE s.ordered DROP CONSTRAINT b_nn", 0)
	execSQL(t, db, "INSERT INTO s.t VALUES(NULL,NULL,NULL,NULL)")
	execSQL(t, db, "RESET SESSION AUTHORIZATION")
	failure := errors.New("ordinary external I/O failure")
	if err := stuntgres.Dump(ctx, db, failingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, failingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type notNullShortWriter struct{}

func (notNullShortWriter) Write([]byte) (int, error) { return 0, nil }

func TestNotNullControlsExternalIOHasNoTargetLock(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	if err := stuntgres.Dump(ctx, db, notNullShortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	writer := &gatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	released := false
	defer func() {
		if !released {
			close(writer.release)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- stuntgres.Dump(ctx, db, writer) }()
	select {
	case <-writer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("dump never reached external I/O")
	}
	execDone := make(chan error, 1)
	go func() { _, err := db.Exec("ALTER TABLE t DROP CONSTRAINT nn"); execDone <- err }()
	select {
	case err := <-execDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Dump held target lock over external I/O")
	}
	close(writer.release)
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dump did not finish")
	}
	if !strings.Contains(writer.output.String(), `CONSTRAINT "nn" NOT NULL`) {
		t.Fatal("capture lost identity", writer.output.String())
	}
	execSQL(t, db, "INSERT INTO t VALUES(NULL)")
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(writer.output.Bytes())); err != nil {
		t.Fatal(err)
	}
	tableCoreExec(t, db, true, "ALTER TABLE t DROP CONSTRAINT nn", 0)
}
