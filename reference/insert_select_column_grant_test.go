//go:build reference

package reference_test

import "testing"

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
