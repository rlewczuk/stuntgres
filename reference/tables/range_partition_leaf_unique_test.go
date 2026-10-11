package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionLeafUnique(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			parent := "rp_unique_parent_" + route.name
			first := "rp_unique_first_" + route.name
			second := "rp_unique_second_" + route.name
			constraint := "rp_unique_" + route.name
			run(fixture.Exec("CREATE TABLE "+parent+"(k INT,payload INT) PARTITION BY RANGE(k)", 0))
			create := fixture.Exec("CREATE TABLE "+first+" PARTITION OF "+parent+"(payload CONSTRAINT "+constraint+" UNIQUE) FOR VALUES FROM(1) TO(3)", 0)
			create.Prepared = route.prepared
			run(create)
			create = fixture.Exec("CREATE TABLE "+second+" PARTITION OF "+parent+"(CONSTRAINT rp_second_"+route.name+" UNIQUE(payload)) FOR VALUES FROM(3) TO(5)", 0)
			create.Prepared = route.prepared
			run(create)
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='"+first+"'::regclass::OID AND contype='u'", []string{"conname"}, []string{"NAME"}, [][]any{{constraint}}))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='"+parent+"'::regclass::OID AND contype='u'", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO "+parent+" VALUES(1,10),(3,20)", 2))
			late := referenceRangeError("INSERT INTO "+parent+" VALUES(4,21),(2,10)", "23505", `duplicate key value violates unique constraint "`+constraint+`"`, fixture.Diagnostic{Detail: "Key (payload)=(10) already exists.", Schema: "public", Table: first, Constraint: constraint})
			late.Prepared = route.prepared
			run(late)
			run(fixture.Select("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(20)}}))
			run(fixture.Select("SELECT * FROM "+second+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(20)}}))
			run(fixture.Prepared(fixture.Exec("INSERT INTO "+first+" VALUES($1,$2)", 1, 2, 20)))
			run(fixture.Select("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(20)}}))
		}
	})
}

func TestReferenceRangePartitionLeafExclusion(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			parent := "rp_ex_parent_" + route.name
			first := "rp_ex_first_" + route.name
			second := "rp_ex_second_" + route.name
			constraint := "rp_ex_" + route.name
			run(fixture.Exec("CREATE TABLE "+parent+"(k INT,payload INT) PARTITION BY RANGE(k)", 0))
			create := fixture.Exec("CREATE TABLE "+first+" PARTITION OF "+parent+"(CONSTRAINT "+constraint+" EXCLUDE USING btree(payload WITH =)) FOR VALUES FROM(1) TO(3)", 0)
			create.Prepared = route.prepared
			run(create)
			run(fixture.Exec("CREATE TABLE "+second+" PARTITION OF "+parent+" FOR VALUES FROM(3) TO(5)", 0))
			run(fixture.Exec("INSERT INTO "+parent+" VALUES(1,10),(3,20)", 2))
			query := "INSERT INTO " + parent + " VALUES($1,$2)"
			fail := fixture.ErrorDiagnostic(query, fixture.ExclusionReferenceConflict(first, constraint, "payload", "10", "10"), 2, 10)
			fail.Prepared, fail.ErrorStage = route.prepared, "execute"
			run(fail)
			run(fixture.ErrorDiagnostic("INSERT INTO "+parent+" VALUES(4,21),(2,10)", fixture.ExclusionReferenceConflict(first, constraint, "payload", "10", "10")))
			rows := fixture.Select("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(20)}})
			run(rows)
			run(fixture.Prepared(rows))
			run(fixture.Exec("INSERT INTO "+first+" VALUES(2,20)", 1))
			run(fixture.Select("SELECT * FROM "+parent+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(20)}}))
		}
		query := "CREATE TABLE rp_ex_virtual(k INT,payload INT,CONSTRAINT rp_ex_virtual_key EXCLUDE USING btree(k WITH =,payload WITH =)) PARTITION BY RANGE(k)"
		if b.PG {
			run(fixture.Exec(query, 0))
			run(fixture.Select("SELECT count(*) AS n FROM rp_ex_virtual", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		} else {
			fail := referenceRangeError(query, "0A000", "partitioned table constraint combination is unsupported", fixture.Diagnostic{})
			fail.ErrorStage = "execute"
			run(fail)
			run(referenceRangeError("SELECT * FROM rp_ex_virtual", "42P01", `relation "rp_ex_virtual" does not exist`, fixture.Diagnostic{Position: "15"}))
		}
	})
}
