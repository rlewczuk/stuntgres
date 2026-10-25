package core_test

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTriggerCorePersistence(t *testing.T) {
	source, scope, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, d, "when_unit", stored.TriggerUpdate)
	syntax, err := parser.Parse("SELECT NEW.v>5")
	if err != nil {
		t.Fatal(err)
	}
	raw := syntax[0].(*parser.Select).Targets[0]
	source.Mu.Lock()
	auth, err := source.AuthorizationLocked(scope.Op)
	var condition expr.Expr
	var query *parser.Select
	var binding analyze.Binding
	if err == nil {
		condition, query, binding, err = (viewcmds.ViewHost{tablecmds.TableHost{core.DDLHost{E: source, Op: scope.Op}}}).BindTriggerWhen(auth, &analyze.RuleNamespace{Relation: relation, Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Event: "UPDATE", Trigger: true}, raw, scope.Input)
	}
	source.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p := definition.Parts()
	p.When = condition
	p.Binding = &binding
	p.Presentation, err = stored.FreezeQueryPresentation(read, ids.SessionToken{}, query, binding, false, false, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := stored.ExpressionDependRefs(scope.Op.Context(), read, condition)
	if err != nil {
		t.Fatal(err)
	}
	p.References = append(p.References, refs...)
	definition, err = stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, source, relation, definition)
	if err := relation.Triggers.Validate(source.CatalogRead(), relation); err != nil {
		t.Fatal(err)
	}
	originalColumn := condition.(plan.ComparisonExpr).Left.(plan.ColumnExpr)
	if originalColumn.Scope != analyze.RulePseudoScope || originalColumn.Frame != 2 || originalColumn.Slot != 1 {
		t.Fatal("WHEN is not a parameterized pseudo-row recipe")
	}

	destination, targetScope, targetRead, target, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	integer, _ := targetRead.LookupType(sqltype.Int4)
	reordered := *target
	reordered.Columns = []catalog.Column{target.Columns[1], target.Columns[0]}
	reordered.Attributes = []int32{2, 1}
	reordered.Index = map[string]int{"v": 0, "id": 1}
	shape, err := sqltype.Composite(target.RowType, target.Name(), "public."+target.Name(), 0, []sqltype.CompositeMember{{Attribute: 2, Name: "v", Usage: integer.Usage()}, {Attribute: 1, Name: "id", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	destination.Mu.Lock()
	state := catalog.CloneState(destination.Catalog.State)
	rowtype := *state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.RowType)})
	rowtype.SQLType = shape
	state.SetDescriptor(rowtype.Address(), &rowtype)
	state.SetDescriptor(reordered.Address(), &reordered)
	destination.Catalog.PublishLocked(state)
	destination.Mu.Unlock()
	targetRead = destination.CatalogRead()
	doubleRef, doubleType := false, false
	mapRef := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) {
			return old
		}
		if old.Domain() == targetRead.Owner().Domain {
			doubleRef = true
		}
		return targetRead.Owner().Domain.Ref(targetRead.Epoch(), old.Address)
	}
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		next, ok := targetRead.LookupType(old.ID())
		if !ok {
			return old
		}
		if old == next {
			doubleType = true
		}
		return next
	}
	mappedOwner := reordered
	mappedOwner.Triggers = relation.Triggers
	principal := catalog.NewProcedureRemapper(destination.RoleStore.PrincipalRemap(&destination.Catalog, source.Roles(), destination.Roles(), nil, routine.OperatorSeeded))
	remap := stored.NewRoutineRemap(targetRead, nil, mapType, principal, false)
	mapped, err := stored.RemapTriggers(targetRead, &mappedOwner, nil, mapRef, remap.Type, remap.Collation, remap.Procedure)
	if err == nil {
		err = remap.Finish()
	}
	if err != nil {
		t.Fatal(err)
	}
	if doubleRef || doubleType {
		t.Fatal("non-idempotent mapper consumed an already mapped identity/type")
	}
	mappedDefinition := stored.TriggersOf(mapped)[0]
	mappedColumn := mappedDefinition.Parts().When.(plan.ComparisonExpr).Left.(plan.ColumnExpr)
	if mappedColumn.Origin.Domain() != targetRead.Owner().Domain || mappedColumn.Slot != 0 || mappedColumn.Origin.Address.SubID != 2 || mappedColumn.ResultUsage().Type != integer {
		t.Fatal("attribute identity, physical slot or type not mapped")
	}
	if condition.(plan.ComparisonExpr).Left.(plan.ColumnExpr) != originalColumn || stored.TriggersOf(relation)[0].Parts().Relation.Domain() != read.Owner().Domain {
		t.Fatal("mapping mutated source snapshot")
	}
	mapped = triggerUnitInstall(t, destination, mapped, mappedDefinition)
	if err := mapped.Triggers.Validate(destination.CatalogRead(), mapped); err != nil {
		t.Fatal(err)
	}
	triggerUnitAdmit(t, targetScope, mapped)
	for _, tc := range []struct {
		value sqltype.Value
		want  plan.Truth
	}{
		{triggerUnitInt(t, integer, 4), plan.False}, {triggerUnitInt(t, integer, 6), plan.True}, {sqltype.Null(integer), plan.Unknown},
	} {
		row := []sqltype.Value{tc.value, triggerUnitInt(t, integer, 1)}
		frame := exec.Context{EvalStatement: exec.EvalStatement{Context: targetScope.Op.Context(), Host: targetScope.WriteHost(), Catalog: targetRead, Input: targetScope.Input, Output: targetScope.Output}, EvalFrame: exec.EvalFrame{Scope: analyze.RulePseudoScope, Frame: 2, Row: row}}
		frame.RoutineAuthority = &targetScope.RoutineAuth
		frame.RoutineRecipe = true
		frame.RoutineChecks = mappedDefinition.Parts().Binding.Dependencies
		if err := exec.AdmitExpression(&frame, mappedDefinition.Parts().When, targetRead); err != nil {
			t.Fatal(err)
		}
		value, err := mappedDefinition.Parts().When.Eval(&frame)
		if err != nil {
			t.Fatal(err)
		}
		truth, err := plan.TruthOf(value)
		if err != nil || truth != tc.want {
			t.Fatal("mapped immutable WHEN row result", truth, err)
		}
	}
	// Clone provenance is a definition identity and a physical attribute map,
	// not a runtime parent-name search. Missing inherited members reject cloning.
	child := reordered
	child.ObjectAddress.ObjectID++
	child.ObjectName = "trigger_unit_child"
	child.Membership = &catalog.PartitionMembership{Parent: mapped.Address()}
	cloneReadState := catalog.CloneState(targetRead.State())
	cloneReadState.SetDescriptor(mapped.Address(), mapped)
	cloneReadState.SetDescriptor(child.Address(), &child)
	cloneRead := catalog.NewRead(targetRead.Owner(), cloneReadState)
	cloneProcedure := catalog.ProcedureRemapper(func(original *catalog.Procedure) (*catalog.Procedure, error) {
		if original == nil {
			return nil, nil
		}
		return exec.ValidateStoredRoutineDescriptor(cloneRead, original)
	})
	cloned, err := stored.CloneTrigger(cloneRead, mappedDefinition, mapped, &child, 9500020, cloneProcedure)
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Parts().Parent != (ids.ObjectAddress{ClassID: 2620, ObjectID: mappedDefinition.OID()}) || cloned.Parts().Relation.Address != child.Address() {
		t.Fatal("clone provenance lost")
	}
	broken := child
	broken.Index = map[string]int{"id": 1}
	if _, err := stored.CloneTrigger(cloneRead, mappedDefinition, mapped, &broken, 9500021, cloneProcedure); err == nil {
		t.Fatal("clone accepted missing referenced member")
	}
}
