package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceNotNullOwnerControls(t *testing.T) {
	roleReferenceCleanup(t, []string{"nn_owner"})
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s")
	p.step(t, referenceExec("CREATE USER nn_owner", 0))
	p.step(t, referenceExec("CREATE SCHEMA s AUTHORIZATION nn_owner", 0))
	text := "O'Brien\n\\tail"
	for _, b := range p.backends() {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("SET SESSION AUTHORIZATION nn_owner", 0))
			run(referenceExec(`CREATE TABLE s.t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
			run(referenceExec("INSERT INTO s.t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), text, true))
			run(referenceExec("INSERT INTO s.t VALUES(8,NULL,NULL,NULL)", 1))
			run(referenceExec("CREATE TABLE s.generated(a INT NOT NULL)", 0))
			run(referenceExec("CREATE TABLE s.ordered(a INT,b INT,CONSTRAINT b_nn NOT NULL b,CONSTRAINT a_nn NOT NULL a)", 0))
			run(referenceExec("COMMENT ON TABLE s.t IS 'persistent'", 0))
			run(referenceExec("COMMENT ON COLUMN s.t.a IS 'attribute'", 0))
		}()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`CONSTRAINT "n""é\\tail" NOT NULL`, `CONSTRAINT "generated_a_not_null" NOT NULL`, `CREATE SCHEMA "s" AUTHORIZATION "nn_owner";`} {
		if !strings.Contains(dump.String(), part) {
			t.Fatal(part, dump.String())
		}
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA s CASCADE"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("owner-bearing stream replay", err)
		}
	}
	expected := referenceSelect("SELECT * FROM s.t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}})
	p.step(t, expected)
	p.step(t, referencePrepared(expected))
	for _, b := range p.backends() {
		referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, expected.query, expected.columns, expected.types, []bool{false, true, true, true}, 2)
		referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, "SELECT * FROM s.ordered", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, false}, 0)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("canonical owner replay", err, again.String())
	}
	for _, b := range p.backends() {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("SET SESSION AUTHORIZATION nn_owner", 0))
			for i, q := range []string{`ALTER TABLE s.t DROP CONSTRAINT "n""é\\tail"`, "ALTER TABLE s.generated DROP CONSTRAINT generated_a_not_null", "ALTER TABLE s.ordered DROP CONSTRAINT a_nn", "ALTER TABLE s.ordered DROP CONSTRAINT b_nn"} {
				s := referenceExec(q, 0)
				s.prepared = i%2 == 0
				run(s)
			}
			run(referenceExec("INSERT INTO s.t VALUES(NULL,NULL,NULL,NULL)", 1))
		}()
	}
	failure := errors.New("ordinary external I/O failure")
	if err := stuntgres.Dump(p.ctx, p.native, referenceNotNullFailingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, referenceNotNullFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type referenceNotNullFailingWriter struct{ err error }

func (w referenceNotNullFailingWriter) Write([]byte) (int, error) { return 0, w.err }

type referenceNotNullFailingReader struct{ err error }

func (r referenceNotNullFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceNotNullRestoreAtomicity(t *testing.T) {
	roleReferenceCleanup(t, []string{"nn_owner"})
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s")
	p.step(t, referenceExec("CREATE USER nn_owner", 0))
	p.step(t, referenceExec("CREATE SCHEMA s AUTHORIZATION nn_owner", 0))
	for _, b := range p.backends() {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("SET SESSION AUTHORIZATION nn_owner", 0))
			run(referenceExec(`CREATE TABLE s.t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
			run(referenceExec("INSERT INTO s.t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien\n\\tail", true))
			run(referenceExec("INSERT INTO s.t VALUES(8,NULL,NULL,NULL)", 1))
			run(referenceExec("CREATE TABLE s.generated(a INT NOT NULL)", 0))
			run(referenceExec("CREATE TABLE s.ordered(a INT,b INT,CONSTRAINT b_nn NOT NULL b,CONSTRAINT a_nn NOT NULL a)", 0))
			run(referenceExec("COMMENT ON TABLE s.t IS 'persistent'", 0))
			run(referenceExec("COMMENT ON COLUMN s.t.a IS 'attribute'", 0))
		}()
	}
	p.step(t, referenceExec(`CREATE TABLE t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
	p.step(t, referenceExec("INSERT INTO t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien\n\\tail", true))
	p.step(t, referenceExec("INSERT INTO t VALUES(8,NULL,NULL,NULL)", 1))
	p.step(t, referenceExec("CREATE TABLE sentinel(v INT)", 0))
	p.step(t, referenceExec("INSERT INTO sentinel VALUES(91)", 1))
	for _, tc := range []struct {
		script    string
		want      referenceDiagnostic
		collision bool
	}{
		{"CREATE TABLE replacement(a INT CONSTRAINT nn NOT NULL);INSERT INTO replacement VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"}, false},
		{"CREATE TABLE replacement(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", referenceDiagnostic{Code: "42710", Message: `constraint "nn" for relation "replacement" already exists`}, false},
		{"CREATE TABLE replacement(a INT,NOT NULL missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "replacement" does not exist`}, false},
		{"CREATE TABLE replacement(a INT NOT NULL,b INT CONSTRAINT replacement_a_not_null NOT NULL)", referenceDiagnostic{}, true},
	} {
		var before bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
			t.Fatal(err)
		}
		rows, err := p.native.QueryContext(p.ctx, "SELECT * FROM sentinel")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.script))
			if tc.collision {
				referenceNotNullNameCollisionDiagnostic(t, err, "replacement_a_not_null")
			} else {
				tc.want.Severity, tc.want.SeverityUnlocalized = "ERROR", "ERROR"
				referenceAssertDiagnostic(t, err, &tc.want)
			}
			if !rows.Next() {
				t.Fatal("retained sentinel missing", rows.Err())
			}
			var n any
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, false, "INT4", n)
			if n != int64(91) {
				t.Fatal(n)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained sentinel changed", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		var after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
			t.Fatal("failed Restore changed target", err)
		}
		p.step(t, referenceSelect("SELECT * FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien\n\\tail", true}, {int64(8), nil, nil, nil}}))
		p.step(t, referenceSelect("SELECT * FROM s.t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien\n\\tail", true}, {int64(8), nil, nil, nil}}))
	}
}
