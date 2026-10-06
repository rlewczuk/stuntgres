//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceInsertSelectIndependentACL(t *testing.T) {
	const writer = "insert_select_writer"
	roleReferenceCleanup(t, []string{writer})
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE ROLE "+writer, 0))
	// Native public schema permits CREATE/USAGE; align the real oracle baseline.
	if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.record, s) }
			run(referenceExec("CREATE TABLE source(i INT)", 0))
			run(referenceExec("CREATE TABLE denied_target(i INT)", 0))
			run(referenceExec("INSERT INTO source VALUES(1)", 1))
			run(referenceExec("SET SESSION AUTHORIZATION "+writer, 0))
			run(referenceExec("CREATE TABLE target(i INT)", 0))
			run(referenceExec("CREATE TABLE own_source(i INT)", 0))
			run(referenceExec("INSERT INTO own_source VALUES(7)", 1))
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ q, table string }{{"INSERT INTO target SELECT i FROM source", "source"}, {"INSERT INTO target SELECT 1 FROM source WHERE FALSE", "source"}, {"INSERT INTO denied_target SELECT i FROM own_source", "denied_target"}} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + tc.table})
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
				s := referenceExec("INSERT INTO target SELECT i FROM own_source", 1)
				s.prepared = prepared
				run(s)
			}
			run(referenceSelect("SELECT i FROM target", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		}()
	}
}

func TestReferenceInsertSelectColumnGrantOracle(t *testing.T) {
	const writer = "insert_select_column_writer"
	roleReferenceCleanup(t, []string{writer})
	p := newReferencePair(t)
	// GRANT is external-only setup: native exposes its actual ACL consumer seam
	// in engine units, not public GRANT syntax or a fabricated host mutation API.
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
	run(referenceExec("CREATE ROLE "+writer, 0))
	run(referenceExec("CREATE TABLE s(i INT,v INT,b BOOL)", 0))
	run(referenceExec("INSERT INTO s VALUES(1,2,TRUE)", 1))
	run(referenceExec("CREATE TABLE t(i INT)", 0))
	for _, q := range []string{"GRANT USAGE ON SCHEMA public TO " + writer, "GRANT INSERT(i) ON t TO " + writer, "GRANT SELECT(i,b) ON s TO " + writer} {
		if _, err := p.postgres.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION "+writer, 0))
	failure := referenceImplicitError("INSERT INTO t SELECT i FROM s WHERE b ORDER BY v", referenceDiagnostic{Code: "42501", Message: "permission denied for table s"})
	failure.errorStage = "execute"
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, failure)
	if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT SELECT(v) ON s TO "+writer); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(referenceExec("INSERT INTO t SELECT i FROM s WHERE b ORDER BY v", 1)))
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("RESET SESSION AUTHORIZATION", 0))
	run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
