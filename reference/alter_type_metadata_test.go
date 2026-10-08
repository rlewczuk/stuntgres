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
			if b.name == "postgres" {
				run(referenceSelect("SELECT attcollation::int FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'", []string{"attcollation"}, []string{"INT4"}, [][]any{{int64(100)}}))
			}
			run(referenceExec("CREATE TABLE nulls(c TEXT)", 0))
			run(referenceExec("INSERT INTO nulls VALUES(NULL),(NULL)", 2))
			run(referenceExec(`ALTER TABLE nulls ALTER c TYPE TEXT COLLATE "POSIX"`, 0))
			run(referenceSelect("SELECT c FROM nulls", []string{"c"}, []string{"TEXT"}, [][]any{{nil}, {nil}}))
			if b.name == "postgres" {
				run(referenceSelect("SELECT attcollation::int FROM pg_attribute WHERE attrelid='nulls'::regclass AND attname='c'", []string{"attcollation"}, []string{"INT4"}, [][]any{{int64(951)}}))
			}
			run(referenceExec(`ALTER TABLE nulls ALTER c TYPE TEXT COLLATE "POSIX", ALTER c TYPE VARCHAR(5) COLLATE "C"`, 0))
			run(referenceSelect("SELECT c FROM nulls", []string{"c"}, []string{"VARCHAR"}, [][]any{{nil}, {nil}}))
			if b.name == "postgres" {
				run(referenceSelect("SELECT attcollation::int FROM pg_attribute WHERE attrelid='nulls'::regclass AND attname='c'", []string{"attcollation"}, []string{"INT4"}, [][]any{{int64(950)}}))
			}
			for _, name := range []string{`public."C"`, `c`, `missing`} {
				run(referenceImplicitError("ALTER TABLE t ALTER c TYPE TEXT COLLATE "+name, referenceDiagnostic{Code: "42704", Message: fmt.Sprintf(`collation "%s" for encoding "UTF8" does not exist`, map[string]string{`public."C"`: "public.C", "c": "c", "missing": "missing"}[name]), Position: "33"}))
			}
			for _, tc := range []struct{ query, code, message, position string }{
				{`ALTER TABLE t ALTER c TYPE INT COLLATE "C"`, "42804", "collations are not supported by type integer", "32"},
				{`ALTER TABLE t ALTER c TYPE TEXT COLLATE missing_schema."C"`, "3F000", `schema "missing_schema" does not exist`, "33"},
			} {
				failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				failure.errorStage = "execute"
				run(failure)
				run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"TEXT"}, [][]any{{"A"}, {"Z"}, {"a"}, {"é"}, {nil}}))
			}
		})
	}
}

func TestReferenceAlterTypeCollationSchemaAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"alter_reader"})
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "denied")
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.ctx, "RESET ROLE")
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
	for _, q := range []string{"CREATE ROLE alter_reader", "CREATE SCHEMA denied", "GRANT USAGE,CREATE ON SCHEMA public TO alter_reader", "SET ROLE alter_reader", "CREATE TABLE t(c TEXT)", "INSERT INTO t VALUES('keep')"} {
		count := int64(0)
		if q == "INSERT INTO t VALUES('keep')" {
			count = 1
		}
		run(referenceExec(q, count))
	}
	failure := referenceImplicitError(`ALTER TABLE t ALTER c TYPE TEXT COLLATE denied."C"`, referenceDiagnostic{Code: "42501", Message: "permission denied for schema denied", Position: "33"})
	failure.errorStage = "execute"
	run(failure)
	run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"keep"}}))
	run(referenceExec("RESET ROLE", 0))
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
