//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterTypeCollation(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE t(c TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(NULL),('a'),('Z'),('A'),('é')", 5))
			for _, tc := range []struct {
				name string
				oid  int64
			}{{`pg_catalog."C"`, 950}, {`pg_catalog."POSIX"`, 951}, {`pg_catalog."default"`, 100}} {
				run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT COLLATE "+tc.name, 0))
				run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"TEXT"}, [][]any{{"A"}, {"Z"}, {"a"}, {"é"}, {nil}}))
				run(referenceSelect("SELECT c FROM t ORDER BY c DESC", []string{"c"}, []string{"TEXT"}, [][]any{{nil}, {"é"}, {"a"}, {"Z"}, {"A"}}))
				if b.name == "postgres" {
					var id int64
					if err := b.db.QueryRowContext(p.ctx, "SELECT attcollation FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'").Scan(&id); err != nil || id != tc.oid {
						t.Fatal(id, tc.oid, err)
					}
				}
			}
			run(referenceExec(`ALTER TABLE t ALTER c TYPE TEXT COLLATE "C"`, 0))
			run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT", 0))
			for _, name := range []string{`public."C"`, `c`, `missing`} {
				run(referenceImplicitError("ALTER TABLE t ALTER c TYPE TEXT COLLATE "+name, referenceDiagnostic{Code: "42704", Message: fmt.Sprintf(`collation "%s" for encoding "UTF8" does not exist`, map[string]string{`public."C"`: "public.C", "c": "c", "missing": "missing"}[name]), Position: "33"}))
			}
		})
	}
}

func TestReferenceAlterTypePhysicalSnapshotBoundary(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE t(c INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7)", 1))
			run(referenceExec("CREATE TABLE anchor(c INT)", 0))
			run(referenceExec("INSERT INTO anchor VALUES(1)", 1))
			tx, err := b.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.record, referenceSelect("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceExec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
			if b.name == "postgres" {
				referenceRunStep(t, p.ctx, tx, b.record, referenceSelect("SELECT c FROM t", []string{"c"}, []string{"INT8"}, nil))
			} else {
				rows, err := tx.QueryContext(p.ctx, "SELECT c FROM t")
				if rows != nil {
					rows.Close()
				}
				transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			}
		})
	}
}
