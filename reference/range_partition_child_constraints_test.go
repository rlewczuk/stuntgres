package reference_test

import "testing"

func TestReferenceRangePartitionChildConstraints(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_local(k INT,payload INT DEFAULT 10,CONSTRAINT rp_parent_check CHECK(payload>0)) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_local_leaf PARTITION OF rp_local(payload NOT NULL DEFAULT 20,CONSTRAINT rp_child_check CHECK(payload<30)) FOR VALUES FROM(1) TO(3)", 0))
		run(referenceExec("INSERT INTO rp_local(k) VALUES(1)", 1))
		run(referencePrepared(referenceExec("INSERT INTO rp_local_leaf(k) VALUES($1)", 1, 2)))
		run(referenceSelect("SELECT * FROM rp_local ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		run(referenceRangeError("INSERT INTO rp_local VALUES(1,11),(2,NULL)", "23502", `null value in column "payload" of relation "rp_local_leaf" violates not-null constraint`, referenceDiagnostic{Detail: "Failing row contains (2, null).", Schema: "public", Table: "rp_local_leaf", Column: "payload"}))
		run(referenceRangeError("INSERT INTO rp_local VALUES(1,11),(2,30)", "23514", `new row for relation "rp_local_leaf" violates check constraint "rp_child_check"`, referenceDiagnostic{Detail: "Failing row contains (2, 30).", Schema: "public", Table: "rp_local_leaf", Constraint: "rp_child_check"}))
		run(referenceRangeError("INSERT INTO rp_local_leaf VALUES(1,0)", "23514", `new row for relation "rp_local_leaf" violates check constraint "rp_parent_check"`, referenceDiagnostic{Detail: "Failing row contains (1, 0).", Schema: "public", Table: "rp_local_leaf", Constraint: "rp_parent_check"}))
		run(referenceSelect("SELECT * FROM rp_local_leaf ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
	})
}
