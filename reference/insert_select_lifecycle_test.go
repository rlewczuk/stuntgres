//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceInsertSelectPreparedRebind(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE s(i INT)", 0))
	p.step(t, referenceExec("CREATE TABLE t(i BIGINT,n NUMERIC(3,1),m NUMERIC(5,2),b BOOL)", 0))
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.record)
		stmt, err := backend.db.PrepareContext(ctx, "INSERT INTO t(i) SELECT $1 FROM s")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			for _, tc := range []struct {
				before               string
				inserted, count, arg int64
			}{{"", 0, 0, 7}, {"INSERT INTO s VALUES(1),(2)", 2, 2, 8}, {"INSERT INTO s VALUES(3)", 1, 3, 9}} {
				if tc.before != "" {
					referenceRunStep(t, ctx, backend.db, backend.record, referenceExec(tc.before, tc.inserted))
				}
				result, err := stmt.ExecContext(ctx, tc.arg)
				if err != nil {
					t.Fatal(err)
				}
				got, err := result.RowsAffected()
				if err != nil || got != tc.count {
					t.Fatal(got, err, tc.count)
				}
			}
			referenceRunStep(t, ctx, backend.db, backend.record, referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(8)}, {int64(9)}, {int64(9)}, {int64(9)}}))
		}()
		referenceRunStep(t, ctx, backend.db, backend.record, referencePrepared(referenceExec("INSERT INTO t(n,m) SELECT $1,$1", 1, "1.234")))
		referenceRunStep(t, ctx, backend.db, backend.record, referenceSelect("SELECT n,m FROM t ORDER BY n", []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.2", "1.23"}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}))
		referenceRunStep(t, ctx, backend.db, backend.record, referencePrepared(referenceExec("INSERT INTO t(b,i) SELECT $1=1,$1 FROM s", 3, int64(1))))
		failure := referenceImplicitError("INSERT INTO t(i) SELECT $1 FROM s WHERE i=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: "25"}, int64(1))
		failure.errorStage = "execute"
		referenceRunStep(t, ctx, backend.db, backend.record, failure)
		failure = referencePrepared(failure)
		failure.errorStage = "prepare"
		referenceRunStep(t, ctx, backend.db, backend.record, failure)
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, e := backend.db.PrepareContext(ctx, "INSERT INTO t(i) SELECT i FROM s")
				if e != nil {
					t.Fatal(e)
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(ctx)
			} else {
				rows, err = backend.db.QueryContext(ctx, "INSERT INTO t(i) SELECT i FROM s")
			}
			if err != nil {
				t.Fatal(err)
			}
			names, err := rows.Columns()
			types, typeErr := rows.ColumnTypes()
			if err != nil || typeErr != nil || len(names) != 0 || len(types) != 0 || rows.Next() || rows.Err() != nil {
				t.Fatal(names, types, err, typeErr, rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		referenceRunStep(t, ctx, backend.db, backend.record, referenceSelect("SELECT i FROM t WHERE i=2", []string{"i"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}}))
	}
}

func TestReferenceInsertSelectTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
			for _, backend := range []struct {
				db     *sql.DB
				record *referenceRecorder
			}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
				tx, err := backend.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, backend.record, s) }
					run(referenceExec("INSERT INTO t VALUES(3)", 1))
					run(referencePrepared(referenceExec("INSERT INTO t SELECT * FROM t", 4)))
					want := [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}}
					run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
					// Independent connection cannot observe the private batch.
					referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}))
					if commit {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						want = [][]any{{int64(1)}, {int64(1)}, {int64(2)}}
					}
					referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
				}()
			}
		})
	}
}

func TestReferenceInsertSelectControls(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE s(i INT,v TEXT)", 0), referenceExec("INSERT INTO s VALUES(1,'x'),(1,'x'),(2,NULL)", 3), referenceExec("CREATE TABLE t(v TEXT,i BIGINT)", 0), referenceExec("INSERT INTO t SELECT v,i FROM s", 3)} {
		p.step(t, s)
	}
	expected := referenceSelect("SELECT * FROM t ORDER BY i", []string{"v", "i"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(1)}, {"x", int64(1)}, {nil, int64(2)}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "SELECT") {
		t.Fatal("query provenance in canonical dump")
	}
	for _, script := range []string{"CREATE TABLE t(i INT); INSERT INTO t SELECT 1", "CREATE TABLE t(i INT); INSERT INTO t SELECT $1", "CREATE TABLE t(i INT); INSERT INTO t VALUES(1) ORDER BY 1"} {
		referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)), &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
		p.step(t, expected)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.step(t, expected)
}

func TestReferenceInsertSelectDefaultBoundary(t *testing.T) {
	p := newReferencePair(t)
	// Declared defaults are a separate native subsystem, not a successful no-op.
	native := referenceImplicitError("CREATE TABLE t(i INT,j INT DEFAULT 42)", referenceDiagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: "28"})
	native.errorStage = "execute"
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("CREATE TABLE t(i INT,j INT DEFAULT 42)", 0))
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO t(i) SELECT 1", 1))
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT * FROM t", []string{"i", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(42)}}))
}
