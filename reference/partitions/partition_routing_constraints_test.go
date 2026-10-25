package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func referenceRoutingConstraintTree(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE pr_constraint(id INT,k INT,payload INT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pr_constraint_low PARTITION OF pr_constraint FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE pr_constraint_high PARTITION OF pr_constraint FOR VALUES FROM(3) TO(5)", 0))
	run(fixture.Exec("ALTER TABLE pr_constraint_low ADD CONSTRAINT pr_source CHECK(k<3)", 0))
	run(fixture.Exec("ALTER TABLE pr_constraint_high ADD CONSTRAINT pr_positive CHECK(payload>0)", 0))
	run(fixture.Exec("ALTER TABLE pr_constraint_high ALTER COLUMN payload SET NOT NULL", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pr_low_payload ON pr_constraint_low(payload)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pr_high_payload ON pr_constraint_high(payload)", 0))
}

func referenceRoutingConstraintFailure(query, kind, proposed string) fixture.Step {
	switch kind {
	case "check":
		return referenceRangeError(query, "23514", `new row for relation "pr_constraint_high" violates check constraint "pr_positive"`, fixture.Diagnostic{Schema: "public", Table: "pr_constraint_high", Constraint: "pr_positive", Detail: "Failing row contains (" + proposed + ")."})
	case "not_null":
		return referenceRangeError(query, "23502", `null value in column "payload" of relation "pr_constraint_high" violates not-null constraint`, fixture.Diagnostic{Schema: "public", Table: "pr_constraint_high", Column: "payload", Detail: "Failing row contains (" + proposed + ")."})
	case "unique":
		return referenceRangeError(query, "23505", `duplicate key value violates unique constraint "pr_high_payload"`, fixture.Diagnostic{Schema: "public", Table: "pr_constraint_high", Constraint: "pr_high_payload", Detail: "Key (payload)=(30) already exists."})
	default:
		panic("unknown routing constraint witness")
	}
}

func TestReferencePartitionRoutingConstraints(t *testing.T) {
	t.Run("physical_checks_and_claims", func(t *testing.T) {
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			referenceRoutingConstraintTree(run)
			run(fixture.Exec("INSERT INTO pr_constraint VALUES(10,2,10),(20,3,30)", 2))
			query := "UPDATE pr_constraint SET k=3,payload=$1 WHERE id=10"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			for _, c := range []struct {
				kind, proposed string
				value          any
			}{{"check", "10, 3, 0", 0}, {"not_null", "10, 3, null", nil}, {"unique", "", 30}} {
				for _, executor := range []fixture.Executor{b.DB, held} {
					s := referenceRoutingConstraintFailure(query, c.kind, c.proposed)
					s.Args = []any{c.value}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					run(fixture.Select("SELECT id,k,payload FROM pr_constraint_low", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(2), int64(10)}}))
					run(fixture.Select("SELECT id,k,payload FROM pr_constraint_high", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(20), int64(3), int64(30)}}))
				}
			}
			// Departure must precede source-only CHECK validation.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, 10))
			run(fixture.Exec("INSERT INTO pr_constraint VALUES(30,2,10)", 1))
			run(referenceRangeError("INSERT INTO pr_constraint VALUES(40,4,10)", "23505", `duplicate key value violates unique constraint "pr_high_payload"`, fixture.Diagnostic{Schema: "public", Table: "pr_constraint_high", Constraint: "pr_high_payload", Detail: "Key (payload)=(10) already exists."}))
			run(fixture.Select("SELECT id,k,payload FROM pr_constraint ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(3), int64(10)}, {int64(20), int64(3), int64(30)}, {int64(30), int64(2), int64(10)}}))
		})
	})
	t.Run("exclusion", func(t *testing.T) {
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			partitionExclusionTree(run, "")
			run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.129','[3,7)')", 2))
			query := "UPDATE pe_root SET tenant=$1 WHERE id=2"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			for _, executor := range []fixture.Executor{b.DB, held} {
				fixture.ExclusionReferenceRun(t, p, b, executor, partitionExclusionConflict(query, "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)", "10.0.0.1"))
				run(fixture.Select("SELECT id FROM pe_low", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT id FROM pe_other", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, "10.0.0.2"))
			run(partitionExclusionIDs(1, 2))
			run(fixture.Select("SELECT id FROM pe_other", []string{"id"}, []string{"INT4"}, nil))
		})
	})
}
