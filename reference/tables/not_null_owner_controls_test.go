package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNotNullOwnerControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nn_owner"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s")
	p.Step(t, fixture.Exec("CREATE USER nn_owner", 0))
	p.Step(t, fixture.Exec("CREATE SCHEMA s AUTHORIZATION nn_owner", 0))
	text := "O'Brien\n\\tail"
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("SET SESSION AUTHORIZATION nn_owner", 0))
			run(fixture.Exec(`CREATE TABLE s.t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
			run(fixture.Exec("INSERT INTO s.t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), text, true))
			run(fixture.Exec("INSERT INTO s.t VALUES(8,NULL,NULL,NULL)", 1))
			run(fixture.Exec("CREATE TABLE s.generated(a INT NOT NULL)", 0))
			run(fixture.Exec("CREATE TABLE s.ordered(a INT,b INT,CONSTRAINT b_nn NOT NULL b,CONSTRAINT a_nn NOT NULL a)", 0))
			run(fixture.Exec("COMMENT ON TABLE s.t IS 'persistent'", 0))
			run(fixture.Exec("COMMENT ON COLUMN s.t.a IS 'attribute'", 0))
		}()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`CONSTRAINT "n""é\\tail" NOT NULL`, `CONSTRAINT "generated_a_not_null" NOT NULL`, `CREATE SCHEMA "s" AUTHORIZATION "nn_owner";`} {
		if !strings.Contains(dump.String(), part) {
			t.Fatal(part, dump.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA s CASCADE"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("owner-bearing stream replay", err)
		}
	}
	expected := fixture.Select("SELECT * FROM s.t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}})
	p.Step(t, expected)
	p.Step(t, fixture.Prepared(expected))
	for _, b := range p.Backends() {
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, expected.Query, expected.Columns, expected.Types, []bool{false, true, true, true}, 2)
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, "SELECT * FROM s.ordered", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, false}, 0)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("canonical owner replay", err, again.String())
	}
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("SET SESSION AUTHORIZATION nn_owner", 0))
			for i, q := range []string{`ALTER TABLE s.t DROP CONSTRAINT "n""é\\tail"`, "ALTER TABLE s.generated DROP CONSTRAINT generated_a_not_null", "ALTER TABLE s.ordered DROP CONSTRAINT a_nn", "ALTER TABLE s.ordered DROP CONSTRAINT b_nn"} {
				s := fixture.Exec(q, 0)
				s.Prepared = i%2 == 0
				run(s)
			}
			run(fixture.Exec("INSERT INTO s.t VALUES(NULL,NULL,NULL,NULL)", 1))
		}()
	}
	failure := errors.New("ordinary external I/O failure")
	if err := stuntgres.Dump(p.Ctx, p.Native, referenceNotNullFailingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, referenceNotNullFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type referenceNotNullFailingWriter struct{ err error }

func (w referenceNotNullFailingWriter) Write([]byte) (int, error) { return 0, w.err }

type referenceNotNullFailingReader struct{ err error }

func (r referenceNotNullFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceNotNullRestoreAtomicity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nn_owner"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s")
	p.Step(t, fixture.Exec("CREATE USER nn_owner", 0))
	p.Step(t, fixture.Exec("CREATE SCHEMA s AUTHORIZATION nn_owner", 0))
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("SET SESSION AUTHORIZATION nn_owner", 0))
			run(fixture.Exec(`CREATE TABLE s.t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
			run(fixture.Exec("INSERT INTO s.t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien\n\\tail", true))
			run(fixture.Exec("INSERT INTO s.t VALUES(8,NULL,NULL,NULL)", 1))
			run(fixture.Exec("CREATE TABLE s.generated(a INT NOT NULL)", 0))
			run(fixture.Exec("CREATE TABLE s.ordered(a INT,b INT,CONSTRAINT b_nn NOT NULL b,CONSTRAINT a_nn NOT NULL a)", 0))
			run(fixture.Exec("COMMENT ON TABLE s.t IS 'persistent'", 0))
			run(fixture.Exec("COMMENT ON COLUMN s.t.a IS 'attribute'", 0))
		}()
	}
	p.Step(t, fixture.Exec(`CREATE TABLE t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien\n\\tail", true))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(8,NULL,NULL,NULL)", 1))
	p.Step(t, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
	for _, tc := range []struct {
		script    string
		want      fixture.Diagnostic
		collision bool
	}{
		{"CREATE TABLE replacement(a INT CONSTRAINT nn NOT NULL);INSERT INTO replacement VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"}, false},
		{"CREATE TABLE replacement(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", fixture.Diagnostic{Code: "42710", Message: `constraint "nn" for relation "replacement" already exists`}, false},
		{"CREATE TABLE replacement(a INT,NOT NULL missing)", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "replacement" does not exist`}, false},
		{"CREATE TABLE replacement(a INT NOT NULL,b INT CONSTRAINT replacement_a_not_null NOT NULL)", fixture.Diagnostic{}, true},
	} {
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		rows, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM sentinel")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
			if tc.collision {
				referenceNotNullNameCollisionDiagnostic(t, err, "replacement_a_not_null")
			} else {
				tc.want.Severity, tc.want.SeverityUnlocalized = "ERROR", "ERROR"
				fixture.AssertDiagnostic(t, err, &tc.want)
			}
			if !rows.Next() {
				t.Fatal("retained sentinel missing", rows.Err())
			}
			var n any
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, false, "INT4", n)
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
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != before.String() {
			t.Fatal("failed Restore changed target", err)
		}
		p.Step(t, fixture.Select("SELECT * FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		p.Step(t, fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien\n\\tail", true}, {int64(8), nil, nil, nil}}))
		p.Step(t, fixture.Select("SELECT * FROM s.t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien\n\\tail", true}, {int64(8), nil, nil, nil}}))
	}
}
