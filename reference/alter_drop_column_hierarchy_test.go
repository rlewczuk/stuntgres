//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterDropColumnExternalHierarchy(t *testing.T) {
	p := newReferencePair(t)
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	record := &referenceRecorder{}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
	run(referenceExec("CREATE TABLE parent(a INT,b TEXT)", 0))
	run(referenceExec("CREATE TABLE inherited() INHERITS(parent)", 0))
	run(referenceExec("CREATE TABLE local(b TEXT) INHERITS(parent)", 0))
	failure := referenceImplicitError("ALTER TABLE inherited DROP b", referenceDiagnostic{Code: "42P16", Message: `cannot drop inherited column "b"`})
	failure.errorStage = "execute"
	run(failure)
	run(referenceExec("ALTER TABLE parent DROP b", 0))
	run(referenceSelect("SELECT * FROM inherited", []string{"a"}, []string{"INT4"}, nil))
	run(referenceSelect("SELECT * FROM local", []string{"a", "b"}, []string{"INT4", "TEXT"}, nil))
	run(referenceExec("CREATE TABLE p1(b INT)", 0))
	run(referenceExec("CREATE TABLE p2(b INT)", 0))
	run(referenceExec("CREATE TABLE multi() INHERITS(p1,p2)", 0))
	run(referenceExec("ALTER TABLE p1 DROP b", 0))
	run(referenceSelect("SELECT * FROM multi", []string{"b"}, []string{"INT4"}, nil))
	run(referenceExec("ALTER TABLE p2 DROP b", 0))
	run(referenceSelect("SELECT * FROM multi", []string{}, []string{}, nil))
	// pg_attribute Table52.7 declares attinhcount int2 (not int4).
	run(referenceExec("CREATE TABLE only_parent(b INT)", 0))
	run(referenceExec("CREATE TABLE only_child() INHERITS(only_parent)", 0))
	run(referenceExec("ALTER TABLE ONLY only_parent DROP b", 0))
	run(referenceSelect("SELECT * FROM only_child", []string{"b"}, []string{"INT4"}, nil))
	run(referenceSelect("SELECT attislocal,attinhcount FROM pg_attribute WHERE attrelid='only_child'::regclass AND attname='b'", []string{"attislocal", "attinhcount"}, []string{"BOOL", "INT2"}, [][]any{{true, int64(0)}}))
	run(referenceExec("ALTER TABLE only_child DROP b", 0))
	run(referenceExec("CREATE TABLE partitioned(k INT,b TEXT) PARTITION BY RANGE(k)", 0))
	run(referenceExec("CREATE TABLE part PARTITION OF partitioned FOR VALUES FROM(0) TO(10)", 0))
	run(referenceExec("INSERT INTO partitioned VALUES(1,'keep')", 1))
	failure = referenceImplicitError("ALTER TABLE ONLY partitioned DROP b", referenceDiagnostic{Code: "42P16", Message: "cannot drop column from only the partitioned table when partitions exist", Hint: "Do not specify the ONLY keyword."})
	failure.errorStage = "execute"
	run(failure)
	failure = referenceImplicitError("ALTER TABLE partitioned DROP k CASCADE", referenceDiagnostic{Code: "42P16", Message: `cannot drop column "k" because it is part of the partition key of relation "partitioned"`})
	failure.errorStage = "execute"
	run(failure)
	run(referenceSelect("SELECT * FROM partitioned", []string{"k", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
	run(referenceExec("ALTER TABLE partitioned DROP b", 0))
	run(referenceSelect("SELECT * FROM part", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
}

func TestReferenceAlterDropColumnTransactionalDDL(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'keep')", 1))
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, conn, backend.record, referenceExec("BEGIN", 0))
		referenceRunStep(t, p.ctx, conn, backend.record, referenceExec("ALTER TABLE t DROP b", 0))
		referenceRunStep(t, p.ctx, conn, backend.record, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		referenceRunStep(t, p.ctx, conn, backend.record, referenceExec("ROLLBACK", 0))
		conn.Close()
	}
	p.step(t, referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
}
