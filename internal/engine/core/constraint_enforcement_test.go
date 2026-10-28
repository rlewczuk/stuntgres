package core_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestConstraintEnforcementCheckProgram(t *testing.T) {
	e := engine.New()
	admin, finish := enginetest.RoleOperation(t, e, "postgres")
	defer finish()
	for _, q := range []string{
		"CREATE ROLE enforcement_program_reader LOGIN",
		"CREATE TABLE public.enforcement_program(j JSON,CONSTRAINT a_hidden CHECK(json_array_length(j)>0) NOT ENFORCED,CONSTRAINT b_live CHECK(j IS NOT NULL) ENFORCED)",
		"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC",
	} {
		enginetest.RoleExecute(t, e, admin, q)
	}
	reader, done := enginetest.RoleOperation(t, e, "enforcement_program_reader")
	defer done()
	scope, err := e.StartStatement(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	r, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		r.Rows.Close()
	}
	view := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, view, "enforcement_program")
	d := view.MustRelation(ref.Address)
	before := slices.Clone(d.Constraints.Check)
	for i := range before {
		before[i].Dependencies = slices.Clone(before[i].Dependencies)
	}
	value, err := d.Columns[0].Usage.Type.Input(context.Background(), "[1]", -1)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: view, Input: scope.Input, Output: scope.Output}}
	failed, err := table.ValidateCheckRow(d, []sqltype.Value{value}, &evaluation)
	if err != nil || failed != nil {
		t.Fatal("unenforced original reached permission preflight", failed, err)
	}
	accepted, ok := evaluation.PreparedChecks[d]
	if !ok || len(accepted.Phases) != 1 {
		t.Fatal("missing real retained program", accepted)
	}
	phase := accepted.Phases[0]
	failed, err = table.ValidateCheckRow(d, []sqltype.Value{value}, &evaluation)
	if err != nil || failed != nil || &evaluation.PreparedChecks[d].Phases[0] != &accepted.Phases[0] || evaluation.PreparedChecks[d].Phases[0].Admission != phase.Admission {
		t.Fatal("same phase lost owned preparation", failed, err)
	}
	if !reflect.DeepEqual(d.Constraints.Check, before) {
		t.Fatal("preparation mutated original manifests")
	}
	// A new genuine definition cannot borrow the former disabled authority.
	enabled := *d
	enabled.Constraints.Check = slices.Clone(d.Constraints.Check)
	enabled.Constraints.Check[0].NotEnforced = false
	_, err = table.ValidateCheckRow(&enabled, []sqltype.Value{value}, &evaluation)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
	if _, ok := evaluation.PreparedChecks[&enabled]; ok {
		t.Fatal("failed readmission retained a partial program")
	}
	if !reflect.DeepEqual(d.Constraints.Check, before) || &evaluation.PreparedChecks[d].Phases[0] != &accepted.Phases[0] {
		t.Fatal("rejected definition damaged accepted owner")
	}
	allFalse := *d
	allFalse.Constraints.Check = []catalog.CheckConstraint{d.Constraints.Check[0]}
	empty := exec.Context{}
	if failed, err := table.ValidateCheckRow(&allFalse, nil, &empty); err != nil || failed != nil || empty.PreparedChecks != nil {
		t.Fatal("all-false path prepared runtime state", failed, err)
	}
}
