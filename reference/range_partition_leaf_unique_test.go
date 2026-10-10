package reference_test

import "testing"

func TestReferenceRangePartitionLeafUnique(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			parent := "rp_unique_parent_" + route.name
			first := "rp_unique_first_" + route.name
			second := "rp_unique_second_" + route.name
			constraint := "rp_unique_" + route.name
			run(referenceExec("CREATE TABLE "+parent+"(k INT,payload INT) PARTITION BY RANGE(k)", 0))
			create := referenceExec("CREATE TABLE "+first+" PARTITION OF "+parent+"(payload CONSTRAINT "+constraint+" UNIQUE) FOR VALUES FROM(1) TO(3)", 0)
			create.prepared = route.prepared
			run(create)
			create = referenceExec("CREATE TABLE "+second+" PARTITION OF "+parent+"(CONSTRAINT rp_second_"+route.name+" UNIQUE(payload)) FOR VALUES FROM(3) TO(5)", 0)
			create.prepared = route.prepared
			run(create)
			run(referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='"+first+"'::regclass::OID AND contype='u'", []string{"conname"}, []string{"NAME"}, [][]any{{constraint}}))
			run(referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='"+parent+"'::regclass::OID AND contype='u'", []string{"conname"}, []string{"NAME"}, nil))
			run(referenceExec("INSERT INTO "+parent+" VALUES(1,10),(3,20)", 2))
			late := referenceRangeError("INSERT INTO "+parent+" VALUES(4,21),(2,10)", "23505", `duplicate key value violates unique constraint "`+constraint+`"`, referenceDiagnostic{Detail: "Key (payload)=(10) already exists.", Schema: "public", Table: first, Constraint: constraint})
			late.prepared = route.prepared
			run(late)
			run(referenceSelect("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(20)}}))
			run(referenceSelect("SELECT * FROM "+second+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(20)}}))
			run(referencePrepared(referenceExec("INSERT INTO "+first+" VALUES($1,$2)", 1, 2, 20)))
			run(referenceSelect("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(20)}}))
		}
	})
}

func TestReferenceRangePartitionLeafExclusion(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			parent := "rp_ex_parent_" + route.name
			first := "rp_ex_first_" + route.name
			second := "rp_ex_second_" + route.name
			constraint := "rp_ex_" + route.name
			run(referenceExec("CREATE TABLE "+parent+"(k INT,payload INT) PARTITION BY RANGE(k)", 0))
			create := referenceExec("CREATE TABLE "+first+" PARTITION OF "+parent+"(CONSTRAINT "+constraint+" EXCLUDE USING btree(payload WITH =)) FOR VALUES FROM(1) TO(3)", 0)
			create.prepared = route.prepared
			run(create)
			run(referenceExec("CREATE TABLE "+second+" PARTITION OF "+parent+" FOR VALUES FROM(3) TO(5)", 0))
			run(referenceExec("INSERT INTO "+parent+" VALUES(1,10),(3,20)", 2))
			query := "INSERT INTO " + parent + " VALUES($1,$2)"
			fail := referenceErrorDiagnostic(query, exclusionReferenceConflict(first, constraint, "payload", "10", "10"), 2, 10)
			fail.prepared, fail.errorStage = route.prepared, "execute"
			run(fail)
			run(referenceErrorDiagnostic("INSERT INTO "+parent+" VALUES(4,21),(2,10)", exclusionReferenceConflict(first, constraint, "payload", "10", "10")))
			rows := referenceSelect("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(20)}})
			run(rows)
			run(referencePrepared(rows))
			run(referenceExec("INSERT INTO "+first+" VALUES(2,20)", 1))
			run(referenceSelect("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(20)}}))
		}
		query := "CREATE TABLE rp_ex_virtual(k INT,payload INT,CONSTRAINT rp_ex_virtual_key EXCLUDE USING btree(k WITH =,payload WITH =)) PARTITION BY RANGE(k)"
		if b.pg {
			run(referenceExec(query, 0))
			run(referenceSelect("SELECT count(*) AS n FROM rp_ex_virtual", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		} else {
			fail := referenceRangeError(query, "0A000", "partitioned table constraint combination is unsupported", referenceDiagnostic{})
			fail.errorStage = "execute"
			run(fail)
			run(referenceRangeError("SELECT * FROM rp_ex_virtual", "42P01", `relation "rp_ex_virtual" does not exist`, referenceDiagnostic{Position: "15"}))
		}
	})
}
