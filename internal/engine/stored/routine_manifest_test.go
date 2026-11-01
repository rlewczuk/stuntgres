package stored

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
		func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
		func(p *catalog.Procedure) { p.Strict = !p.Strict },
		func(p *catalog.Procedure) { p.Language.ObjectID++ },
		func(p *catalog.Procedure) { p.SQLBody = NewRoutineProgram(nil) },
		func(p *catalog.Procedure) { p.SQLDefaults = &RoutineDefaults{} },
	} {
		forged := current
		mutate(&forged)
		read.State().SetDescriptor(forged.Address(), &forged)
		if err := ValidateRoutineCallIdentities(read, binding); err == nil {
			t.Fatal("changed immutable declaration admitted", forged)
		}
	}
	read.State().SetDescriptor(current.Address(), &current)
	for _, kind := range []string{"foreign", "stale-epoch", "subobject", "wrong-address", "wrong-result"} {
		t.Run(kind, func(t *testing.T) {
			// A fresh authentic tree prevents one deliberately corrupt occurrence
			// from becoming another negative case's source authority.
			source, program, declaration := routineMergeCaptureFixture(t)
			b := program.Action(0).Binding()
			call := b.Merge.Arms[0].Input.Update.Assignments[0].(plan.SQLRoutineExpr)
			switch kind {
			case "foreign":
				call.Ref = new(ids.Domain).Ref(source.Epoch(), declaration.Address())
			case "stale-epoch":
				call.Ref = source.Owner().Domain.Ref(source.Epoch()+1, declaration.Address())
			case "subobject":
				call.Ref.Address.SubID = 1
			case "wrong-address":
				call.Ref.Address.ObjectID++
			case "wrong-result":
				call.Signature.Result = source.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType.Usage()
			}
			b.Merge.Arms[0].Input.Update.Assignments[0] = call
			if err := ValidateRoutineCallIdentities(source, b); err == nil {
				t.Fatal("invalid original occurrence admitted")
			}
		})
	}
}
