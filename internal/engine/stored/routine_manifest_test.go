package stored

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
)

func TestRoutineAlterFrozenManifestRetention(t *testing.T) {
	read, program, captured := routineMergeCaptureFixture(t)
	binding := program.Action(0).Binding()
	original := binding.Merge.Arms[0].Input.Update.Assignments[0].(plan.SQLRoutineExpr)
	manifest := binding.Dependencies[0]
	current := *captured
	current.ObjectName = "renamed_dormant"
	current.ObjectRevision++
	read.State().SetDescriptor(current.Address(), &current)
	if err := ValidateRoutineCallIdentities(read, binding); err != nil {
		t.Fatal("mutable revision rejected hard capture", err)
	}
	call := binding.Merge.Arms[0].Input.Update.Assignments[0].(plan.SQLRoutineExpr)
	if call.Procedure != captured || call.Info.RoutineReference != captured || call.Ref != original.Ref || binding.Dependencies[0] != manifest || call.Procedure.Revision() != manifest.Revision {
		t.Fatal("validation mixed current descriptor with original lifetime manifest")
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Result++ },
		func(p *catalog.Procedure) { definition := *p.SQL; p.SQL = &definition },
		func(p *catalog.Procedure) { p.SQL = &proc.Definition{} },
		func(p *catalog.Procedure) { p.Signature = "23" },
	} {
		forged := current
		mutate(&forged)
		read.State().SetDescriptor(forged.Address(), &forged)
		if err := ValidateRoutineCallIdentities(read, binding); err == nil {
			t.Fatal("changed immutable declaration admitted", forged)
		}
	}
}
