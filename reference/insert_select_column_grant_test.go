package reference_test

import "testing"

func TestReferenceInsertSelectColumnGrantOracle(t *testing.T) {
	const writer = "insert_select_column_writer"
	roleReferenceCleanup(t, []string{writer})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
	if backend.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO PUBLIC",0)) }
	run(referenceExec("CREATE ROLE "+writer, 0))
	run(referenceExec("CREATE TABLE s(i INT,v INT,b BOOL)", 0))
	run(referenceExec("INSERT INTO s VALUES(1,2,TRUE)", 1))
	run(referenceExec("CREATE TABLE t(i INT)", 0))
	for _, q := range []string{"GRANT INSERT(i) ON t TO " + writer, "GRANT SELECT(i,b) ON s TO " + writer} {
		if _, err := backend.db.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET SESSION AUTHORIZATION "+writer, 0))
	failure := referenceImplicitError("INSERT INTO t SELECT i FROM s WHERE b ORDER BY v", referenceDiagnostic{Code: "42501", Message: "permission denied for table s"})
	failure.errorStage = "execute"
	referenceRunStep(t, p.ctx, conn, backend.recorder, failure)
	if _, err := backend.db.ExecContext(p.ctx, "GRANT SELECT(v) ON s TO "+writer); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(referenceExec("INSERT INTO t SELECT i FROM s WHERE b ORDER BY v", 1)))
	referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("RESET SESSION AUTHORIZATION", 0))
	run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
}
