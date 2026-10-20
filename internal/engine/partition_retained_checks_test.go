package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestHashPartitionRetainedCheckPolicy(t *testing.T) {
	e, view, root, leaf := hashPartitionUnitEngine(t)
	typ := leaf.Columns[0].Usage.Type
	root.Columns = append(root.Columns, catalog.Column{Name: "payload", Usage: typ.Usage(), Nullable: true})
	root.Attributes = append(root.Attributes, 2)
	root.Index["payload"] = 1
	root.ColumnMeta.HighWater = 2
	leaf.ColumnMeta.HighWater = 2
	leaf.Columns = root.Columns
	leaf.Attributes = root.Attributes
	leaf.Index = root.Index
	boolean, _ := view.LookupType(sqltype.Bool)
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	makeCheck := func(oid ids.OID, name string, slot int, operator sqltype.ComparisonOperator, n int64) catalog.CheckConstraint {
		ref := view.Owner().Domain.Ref(view.Epoch(), leaf.Address())
		ref.Address.SubID = leaf.Attributes[slot]
		column := columnExpression{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: slot}
		literal := literalExpression{Info: expr.Info{Usage: typ.Usage()}, value: integer(n)}
		predicate, err := sqltype.BindComparisonPredicate(operator, typ, typ, boolean, sqltype.CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		expression := comparisonExpression{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, left: column, right: literal, predicate: predicate}
		return catalog.CheckConstraint{OID: oid, Name: name, NotValid: true, Expression: expression, Dependencies: []catalog.ExpressionDependency{{Ref: ref}}}
	}
	leaf.Constraints.Check = []catalog.CheckConstraint{makeCheck(26000, "old_payload", 1, sqltype.CompareNotEqual, 0), makeCheck(26001, "changed_key", 0, sqltype.CompareEqual, 1)}
	oldValues := []sqltype.Value{integer(1), integer(0)}
	dead := storage.RowVersion{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{integer(99), integer(99)}}
	live := storage.RowVersion{Row: 2, Version: 2, Born: 3, Values: oldValues}
	store := &storage.Relation{Versions: []storage.RowVersion{dead, live}, Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 1, End: 2}}}}
	e.catalog.state = view.State()
	e.visibility.SetCommitSeqLocked(3)
	e.mu.Lock()
	e.storage.ReplaceRelationsLocked(map[ids.ObjectAddress]*storage.Relation{leaf.Address(): store})
	e.mu.Unlock()
	snapshot, err := e.captureIndexSnapshot(context.Background(), view, e.storage.Relations, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.sources) != 0 || len(snapshot.partitionRows) != 1 {
		t.Fatal("unindexed CHECK history was not captured", snapshot)
	}
	history := snapshot.partitionRows[leaf.Address()]
	if &history.store.Versions[0] != &store.Versions[0] {
		t.Fatal("original immutable versions were needlessly copied")
	}
	position := 0
	before, err := history.nextRow(context.Background(), &position, live)
	if err != nil || position != 2 || before[0] != oldValues[0] {
		t.Fatal("deleted source version changed old-row matching", position, before, err)
	}
	position = 0
	if _, err := history.nextRow(context.Background(), &position, storage.RowVersion{Row: 9, Version: 9}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("mismatched source identity accepted", err)
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background(), catalog: view}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	var policy partitionRetainedChecks
	validate := func(values []sqltype.Value, want string) {
		t.Helper()
		d, err := policy.forRow(evaluation.Context, history, leaf, before, values, snapshot, &evaluation)
		if err != nil {
			t.Fatal(err)
		}
		err = validatePhysicalRowConstraints(d, "public", values, &evaluation, func() (string, error) { return "test row", nil })
		if want == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Constraint != want {
			t.Fatal("wrong selected CHECK enforcement", err, want)
		}
	}
	validate(oldValues, "")
	var foreign *sqltype.Descriptor
	for _, candidate := range sqltype.Builtins() {
		if candidate.ID() == sqltype.Int4 {
			foreign = candidate
			break
		}
	}
	one, err := sqltype.Integer(foreign, 1)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := sqltype.Integer(foreign, 0)
	if err != nil {
		t.Fatal(err)
	}
	validate([]sqltype.Value{one, zero}, "")                         // Descriptor-only reownership is not a proposal.
	validate([]sqltype.Value{integer(2), integer(0)}, "changed_key") // Unrelated old_payload remains historical.
	validate([]sqltype.Value{integer(1), integer(1)}, "")            // Relevant changed payload is checked and now satisfies its predicate.
	// Real movement uses the ordinary full destination validator, not the
	// retained-row subset, and must reject even an old NOT VALID violation.
	err = validatePhysicalRowConstraints(leaf, "public", oldValues, &evaluation, func() (string, error) { return "test row", nil })
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Constraint != "old_payload" {
		t.Fatal("full destination validation lost NOT VALID enforcement", err)
	}
	// A virtual generated CHECK input has no stored payload; its stored
	// dependency cells still determine whether this old constraint is affected.
	virtualLeaf := *leaf
	sourceColumn := leaf.Constraints.Check[1].Expression.(comparisonExpression).left.(columnExpression)
	virtualLeaf.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: sourceColumn, Planned: sourceColumn, Dependencies: []ids.ObjectRef{sourceColumn.origin}}}
	virtualCheck := makeCheck(26002, "virtual_input", 1, sqltype.CompareEqual, 1)
	virtualExpression := virtualCheck.Expression.(comparisonExpression)
	virtualColumn := virtualExpression.left.(columnExpression)
	virtualColumn = virtualColumn.withGeneration(&virtualLeaf, 1, 0)
	virtualExpression.left = virtualColumn
	virtualCheck.Expression = virtualExpression
	virtualLeaf.Constraints.Check = []catalog.CheckConstraint{virtualCheck}
	virtualHistory := history
	virtualHistory.relation = &virtualLeaf
	virtualBefore := []sqltype.Value{integer(1), sqltype.Null(typ)}
	virtualAfter := []sqltype.Value{integer(2), sqltype.Null(typ)}
	var virtualPolicy partitionRetainedChecks
	virtualDescriptor, err := virtualPolicy.forRow(evaluation.Context, virtualHistory, &virtualLeaf, virtualBefore, virtualAfter, snapshot, &evaluation)
	if err != nil {
		t.Fatal(err)
	}
	err = validatePhysicalRowConstraints(virtualDescriptor, "public", virtualAfter, &evaluation, func() (string, error) { return "test row", nil })
	if !errors.As(err, &diagnostic) || diagnostic.Constraint != "virtual_input" {
		t.Fatal("changed stored input of virtual generation was ignored", err)
	}
	// A domain cast's provider/descriptor renumber alone is not changed data.
	oldDomain, err := sqltype.Domain(28000, "wrapped", "public.wrapped", typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	newDomain, err := sqltype.Domain(28001, "wrapped", "public.wrapped", typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	oldPlan, err := sqltype.PlanCoercion(typ.Usage(), oldDomain.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	newPlan, err := sqltype.PlanCoercion(typ.Usage(), newDomain.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	literal := literalExpression{Info: expr.Info{Usage: typ.Usage()}, value: integer(0)}
	oldCast := coercionExpression{Info: expr.Info{Usage: oldDomain.Usage()}, child: literal, plan: oldPlan, mode: sqltype.Explicit}
	newCast := coercionExpression{Info: expr.Info{Usage: newDomain.Usage()}, child: literal, plan: newPlan, mode: sqltype.Explicit}
	if affected, err := partitionCheckRecipeAffected(evaluation.Context, oldCast, newCast, false); err != nil || affected {
		t.Fatal("domain identity-only remap revalidated an old NOT VALID check", affected, err)
	}
	regclass, _ := view.LookupType(sqltype.Regclass)
	oldReference, err := regclass.Input(evaluation.Context, "42", -1)
	if err != nil {
		t.Fatal(err)
	}
	newReference, err := regclass.Input(evaluation.Context, "43", -1)
	if err != nil {
		t.Fatal(err)
	}
	beforeLiteral := literalExpression{Info: expr.Info{Usage: regclass.Usage()}, value: oldReference}
	afterLiteral := literalExpression{Info: expr.Info{Usage: regclass.Usage()}, value: newReference}
	if affected, err := partitionCheckRecipeAffected(evaluation.Context, beforeLiteral, afterLiteral, false); err != nil || !affected {
		t.Fatal("material reminted literal input was ignored", affected, err)
	}
	oidType, _ := view.LookupType(sqltype.Oid)
	ownerValue := tableOIDExpression{Info: expr.Info{Usage: oidType.Usage()}, owner: view.Owner().Domain.Ref(view.Epoch(), leaf.Address())}
	if affected, err := partitionCheckRecipeAffected(evaluation.Context, ownerValue, ownerValue, true); err != nil || !affected {
		t.Fatal("owner-sensitive recipe input was ignored", affected, err)
	}
	// An actual changed stored literal selects and enforces only its CHECK,
	// despite unchanged row cells and an unrelated old violating constraint.
	literalLeaf := *leaf
	literalLeaf.Constraints.Check = append([]catalog.CheckConstraint(nil), leaf.Constraints.Check...)
	literalCheck := literalLeaf.Constraints.Check[1].Expression.(comparisonExpression)
	changedConstant := literalCheck.right.(literalExpression)
	changedConstant.value = integer(2)
	literalCheck.right = changedConstant
	literalLeaf.Constraints.Check[1].Expression = literalCheck
	var literalPolicy partitionRetainedChecks
	literalDescriptor, err := literalPolicy.forRow(evaluation.Context, history, &literalLeaf, oldValues, oldValues, snapshot, &evaluation)
	if err != nil {
		t.Fatal(err)
	}
	err = validatePhysicalRowConstraints(literalDescriptor, "public", oldValues, &evaluation, func() (string, error) { return "test row", nil })
	if !errors.As(err, &diagnostic) || diagnostic.Constraint != "changed_key" {
		t.Fatal("changed stored literal did not enforce its CHECK", err)
	}
	// Mapping changes the candidate lookup key, not its immutable original row authority.
	destination := leaf.Address()
	destination.ObjectID = 27000
	snapshot.captureMapping(map[ids.ObjectAddress]ids.ObjectAddress{leaf.Address(): destination}, nil)
	if snapshot.partitionRows[destination].relation != leaf || snapshot.partitionRows[leaf.Address()].relation != nil {
		t.Fatal("row history address mapping lost provenance")
	}
}
