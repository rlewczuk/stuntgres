package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestArrayAggregateDescription(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT array_agg(id ORDER BY id),array_agg(NULL::integer[]) FROM t")
	defer source.Close()
	for _, column := range query.Root.Schema() {
		if column.Name != "array_agg" || column.Usage.Type.ID() != 1007 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || !column.Nullable || !column.NullabilityKnown {
			t.Fatal("aggregate description", column)
		}
	}
}

func TestIntaggArrayAggregateSupportRecipe(t *testing.T) {
	e := engine.New()
	for _, query := range []string{
		"CREATE EXTENSION intagg",
		"CREATE TABLE intagg_support_rows(v integer)",
		"INSERT INTO intagg_support_rows VALUES(2),(NULL),(1)",
		"CREATE VIEW intagg_support_recipe AS SELECT int_array_aggregate(v) AS a FROM intagg_support_rows",
	} {
		enginetest.Run(t, e, query)
	}
	consumer := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "intagg_support_recipe").Address)
	presentation := stored.ViewOf(consumer)
	node := presentation.Query().Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	call := node.Calls[0]
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err == nil {
		scope.RoutineAuth = auth
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: auth.Catalog, Host: core.HostOf(scope)},
		EvalAdmission: exec.EvalAdmission{RoutineRecipe: true, RoutineChecks: presentation.Dependencies(), RoutineConsumer: consumer},
	}
	if call.RoutineRecipe != nil {
		t.Fatal("ordinary stored source unexpectedly requires a graft")
	}
	var supports []catalog.ExpressionDependency
	for _, dependency := range presentation.Dependencies() {
		if dependency.Purpose == catalog.AggregateSupportDependency {
			supports = append(supports, dependency)
		}
	}
	if len(supports) != 2 {
		t.Fatal("genuine helper support lanes were not captured", supports)
	}
	if err := exec.AdmitAggregateSupport(&evaluation, &call); err != nil {
		t.Fatal("array provider discarded its inherited stored support manifest", err)
	}
	for _, mode := range []string{"missing", "wrong-purpose", "foreign", "revision"} {
		probe := evaluation
		probe.RoutineChecks = append([]catalog.ExpressionDependency(nil), presentation.Dependencies()...)
		for i := range probe.RoutineChecks {
			dependency := &probe.RoutineChecks[i]
			if dependency.Ref != supports[0].Ref {
				continue
			}
			switch mode {
			case "missing":
				probe.RoutineChecks = append(probe.RoutineChecks[:i], probe.RoutineChecks[i+1:]...)
			case "wrong-purpose":
				dependency.Purpose = catalog.DefinitionDependency
			case "foreign":
				dependency.Ref = new(ids.Domain).Ref(dependency.Ref.Epoch, dependency.Ref.Address)
			case "revision":
				dependency.Revision++
			}
			break
		}
		if err := exec.AdmitAggregateSupport(&probe, &call); err == nil {
			t.Fatal("invalid actual helper support manifest accepted", mode)
		}
	}
}
