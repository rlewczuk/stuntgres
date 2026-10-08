package reference_test

import (
	"context"
	"database/sql/driver"
	"testing"
)

func TestReferenceInsertSelectPreparedRebind(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			run := func(s referenceStep) { insertSelectRun(t, ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE s(i INT)", 0))
			run(referenceExec("CREATE TABLE t(i BIGINT,n NUMERIC(3,1),m NUMERIC(5,2),b BOOL)", 0))
			query := "INSERT INTO t(i) SELECT $1 FROM s"
			stmt, err := backend.db.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{backend.db, stmt, query}
			for _, tc := range []struct {
				before               string
				inserted, count, arg int64
			}{
				{"", 0, 0, 7}, {"INSERT INTO s VALUES(1),(2)", 2, 2, 8}, {"INSERT INTO s VALUES(3)", 1, 3, 9},
			} {
				if tc.before != "" {
					run(referenceExec(tc.before, tc.inserted))
				}
				insertSelectRun(t, ctx, held, backend.recorder, referenceExec(query, tc.count, tc.arg))
			}
			run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(8)}, {int64(9)}, {int64(9)}, {int64(9)}}))
			run(referencePrepared(referenceExec("INSERT INTO t(n,m) SELECT $1,$1", 1, "1.234")))
			run(referenceSelect("SELECT n,m FROM t ORDER BY n", []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.2", "1.23"}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}))
			run(referencePrepared(referenceExec("INSERT INTO t(b,i) SELECT $1=1,$1 FROM s", 3, int64(1))))
			failure := referenceImplicitError("INSERT INTO t(i) SELECT $1 FROM s WHERE i=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: "25"}, int64(1))
			failure.errorStage = "execute"
			run(failure)
			failure.prepared, failure.errorStage = true, "prepare"
			run(failure)
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("INSERT INTO t(i) SELECT i FROM s", []string{}, []string{}, [][]any{})
				s.prepared = prepared
				run(s)
			}
			run(referenceSelect("SELECT i FROM t WHERE i=2", []string{"i"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}}))
		})
	}
}

func TestReferenceInsertSelectCommandMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE s(i INT)", 0), referenceExec("CREATE TABLE t(i INT)", 0)} {
		p.step(t, s)
	}
	// Raw native statement metadata is a public driver contract, before wrapping.
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		stmt, err := raw.(driver.Conn).Prepare("INSERT INTO t SELECT $1 FROM s")
		if err != nil {
			return err
		}
		defer stmt.Close()
		if stmt.NumInput() != 1 {
			t.Fatalf("NumInput = %d", stmt.NumInput())
		}
		for _, tc := range []struct {
			before               string
			inserted, count, arg int64
		}{
			{"", 0, 0, 7}, {"INSERT INTO s VALUES(1),(2)", 2, 2, 8}, {"INSERT INTO s VALUES(3)", 1, 3, 9},
		} {
			if tc.before != "" {
				insertSelectRun(t, p.ctx, p.memory, p.memoryRecord, referenceExec(tc.before, tc.inserted))
			}
			result, err := stmt.Exec([]driver.Value{tc.arg})
			insertSelectAffected(t, result, err, tc.count)
		}
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				run := func(s referenceStep) { insertSelectRun(t, p.ctx, backend.db, backend.recorder, s) }
				if backend.pg {
					run(referenceExec("INSERT INTO s VALUES(1),(2),(3)", 3))
				}
				for _, prepared := range []bool{false, true} {
					s := referenceSelect("INSERT INTO t SELECT i FROM s", []string{}, []string{}, [][]any{})
					s.prepared = prepared
					run(s)
				}
				run(referenceSelect("SELECT i FROM t WHERE i=2", []string{"i"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			})
		}
		insertSelectRun(t, p.ctx, p.memory, p.memoryRecord, referenceExec("ALTER TABLE s RENAME COLUMN i TO renamed", 0))
		result, err := stmt.Exec([]driver.Value{int64(10)})
		insertSelectAffected(t, result, err, 3)
		named, err := raw.(driver.Conn).Prepare("INSERT INTO t SELECT renamed FROM s")
		if err != nil {
			return err
		}
		defer named.Close()
		insertSelectRun(t, p.ctx, p.memory, p.memoryRecord, referenceExec("ALTER TABLE s RENAME COLUMN renamed TO changed", 0))
		result, err = named.Exec(nil)
		if result != nil {
			t.Fatal("failed raw Exec exposed a result")
		}
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "renamed" does not exist`, Position: "22"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReferenceInsertSelectSourceRename(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			run := func(s referenceStep) { insertSelectRun(t, ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE s(i INT)", 0))
			run(referenceExec("CREATE TABLE t(i INT)", 0))
			query := "INSERT INTO t SELECT $1 FROM s"
			stmt, err := backend.db.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{backend.db, stmt, query}
			for _, tc := range []struct {
				before               string
				inserted, count, arg int64
			}{
				{"", 0, 0, 7}, {"INSERT INTO s VALUES(1),(2)", 2, 2, 8}, {"INSERT INTO s VALUES(3)", 1, 3, 9},
			} {
				if tc.before != "" {
					run(referenceExec(tc.before, tc.inserted))
				}
				insertSelectRun(t, ctx, held, backend.recorder, referenceExec(query, tc.count, tc.arg))
			}
			run(referenceExec("ALTER TABLE s RENAME COLUMN i TO renamed", 0))
			insertSelectRun(t, ctx, referenceHeldStatement{backend.db, stmt, query}, backend.recorder, referenceExec(query, 3, int64(10)))
			query = "INSERT INTO t SELECT renamed FROM s"
			named, err := backend.db.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer named.Close()
			run(referenceExec("ALTER TABLE s RENAME COLUMN renamed TO changed", 0))
			failure := referenceImplicitError(query, referenceDiagnostic{Code: "42703", Message: `column "renamed" does not exist`, Position: "22"})
			failure.errorStage = "execute"
			insertSelectRun(t, ctx, referenceHeldStatement{backend.db, named, query}, backend.recorder, failure)
			run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(8)}, {int64(9)}, {int64(9)}, {int64(9)}, {int64(10)}, {int64(10)}, {int64(10)}}))
		})
	}
}

func TestReferenceInsertSelectTransactions(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "failure"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { insertSelectRun(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(i INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
					tx, err := backend.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					own := func(s referenceStep) { insertSelectRun(t, p.ctx, tx, backend.recorder, s) }
					own(referenceExec("INSERT INTO t VALUES(3)", 1))
					own(referencePrepared(referenceExec("INSERT INTO t SELECT * FROM t", 4)))
					want := [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}}
					own(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
					committed := [][]any{{int64(1)}, {int64(1)}, {int64(2)}}
					run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, committed))
					if mode == "commit" {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					} else {
						if mode == "failure" {
							s := referenceImplicitError("INSERT INTO t SELECT 'bad'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "22"})
							s.errorStage = "execute"
							own(s)
							s = referenceImplicitError("SELECT i FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
							s.errorStage = "execute"
							own(s)
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						want = committed
					}
					run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
				})
			}
		})
	}
}
