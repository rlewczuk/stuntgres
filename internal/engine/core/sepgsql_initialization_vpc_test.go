package core_test

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
)

func TestSepgsqlSimulationInitializationVPC(t *testing.T) {
	e := sepgsqlInitializationEngine(t)
	_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, `SELECT key FROM skeys('a=>1,b=>2'::hstore) AS s(key)`)
	if evaluation.Input.LockedDomainInput != scope || evaluation.Input.DomainChecks != scope {
		t.Fatal("literal source input lost its genuine statement binder owner")
	}
	from, ok := project.Child.(*plan.FunctionFromNode)
	if !ok || len(from.Calls) != 1 {
		t.Fatal("missing genuine VPC source")
	}
	call := from.Calls[0]
	if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
		t.Fatal(err)
	}
	site := sepgsqlInitializationSite(t, evaluation, call)
	host, owner := core.HostOf(scope), evaluation.RoutineInitialization
	actor, err := host.ValidateRoutineInitialization(owner, site)
	if err != nil {
		t.Fatal("missing original VPC source receipt", err)
	}
	mark := scope.MarkSepgsqlObservations()
	// Actual first FHET entry observes the selected procedure's label basis
	// (mask zero), separately from the already completed execute admission.
	// Subsequent entries deduplicate that same basis; none completes a receipt.
	bodyMark := mark
	bodyMark.Observations++
	open, ok := call.(expr.SourceOpening)
	if !ok {
		t.Fatal("selected source lacks its real opener")
	}
	advance, ok := call.(expr.SourceAdvancing)
	if !ok {
		t.Fatal("selected source lacks its VPC owner")
	}
	cursor, err := open.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if current := scope.MarkSepgsqlObservations(); current != mark {
		t.Fatalf("argument-only source opening changed admission: before=%+v after=%+v", mark, current)
	}
	defer cursor.Close()
	found := map[string]int{}
	for range 2 {
		value, present, err := advance.AdvanceSource(evaluation.Context, evaluation, &cursor)
		if err != nil || !present {
			t.Fatal("actual VPC row", present, err)
		}
		text, err := value.Text()
		if err != nil {
			t.Fatal(err)
		}
		found[text]++
		if current := scope.MarkSepgsqlObservations(); current != bodyMark {
			t.Fatalf("VPC row changed receipt watermarks or failed label-basis deduplication: before=%+v expected=%+v after=%+v", mark, bodyMark, current)
		}
		if evaluation.RoutineInitialization != owner {
			t.Fatal("VPC row replaced its original initialization owner")
		}
		if currentActor, err := host.ValidateRoutineInitialization(owner, site); err != nil || currentActor != actor {
			t.Fatal("VPC row lost its original source receipt actor", currentActor, actor, err)
		}
	}
	if found["a"] != 1 || found["b"] != 1 || len(found) != 2 {
		t.Fatal("real source bag", found)
	}
	if _, present, err := advance.AdvanceSource(evaluation.Context, evaluation, &cursor); err != nil || present {
		t.Fatal("actual consumed terminal DONE", present, err)
	}
	if evaluation.RoutineInitialization != owner {
		t.Fatal("terminal DONE replaced its original initialization owner")
	}
	if currentActor, err := host.ValidateRoutineInitialization(owner, site); err != nil || currentActor != actor {
		t.Fatal("VPC consumed original reusable source receipt", currentActor, actor, err)
	}
	if current := scope.MarkSepgsqlObservations(); current != bodyMark {
		t.Fatalf("terminal DONE changed retained body evidence or receipt watermarks: expected=%+v after=%+v", bodyMark, current)
	}
	cursor, err = open.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	lost := *evaluation
	lost.RoutineInitialization = exec.RoutineInitialization{}
	if _, _, err = advance.AdvanceSource(evaluation.Context, &lost, &cursor); err == nil {
		t.Fatal("VPC row adopted current Scope owner after copied Context lost it")
	}
	if !cursor.Exhausted || cursor.State != nil {
		t.Fatal("denied VPC did not release source")
	}
}
