package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestHashPartitionRetainedCheckPolicy(t *testing.T) {
	catalog, root, leaf := hashPartitionUnitDescriptor(t)
	typ := leaf.columns[0].Usage.Type
	root.columns = append(root.columns, ColumnDescriptor{Name: "payload", Usage: typ.Usage(), Nullable: true})
	root.attributes = append(root.attributes, 2)
	root.index["payload"] = 1
	root.attributeHighWater = 2
	leaf.attributeHighWater = 2
	leaf.columns = root.columns
	leaf.attributes = root.attributes
	leaf.index = root.index
	boolean, _ := catalog.LookupType(sqltype.Bool)
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	makeCheck := func(oid ids.OID, name string, slot int, operator sqltype.ComparisonOperator, n int64) checkConstraint {
		ref := catalog.owner.catalog.domain.Ref(catalog.Epoch(), leaf.address)
		ref.Address.SubID = leaf.attributes[slot]
		column := columnExpression{expressionInfo: expressionInfo{Usage: typ.Usage(), Property: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: slot}
		literal := literalExpression{expressionInfo: expressionInfo{Usage: typ.Usage()}, value: integer(n)}
		predicate, err := sqltype.BindComparisonPredicate(operator, typ, typ, boolean, sqltype.CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		expression := comparisonExpression{expressionInfo: expressionInfo{Usage: boolean.Usage(), Property: ExprProperties{RowDependent: true}}, left: column, right: literal, predicate: predicate}
		return checkConstraint{OID: oid, Name: name, NotValid: true, expression: expression, dependencies: []expressionDependency{{ref: ref}}}
	}
	leaf.checkConstraints = []checkConstraint{makeCheck(26000, "old_payload", 1, sqltype.CompareNotEqual, 0), makeCheck(26001, "changed_key", 0, sqltype.CompareEqual, 1)}
	oldValues := []sqltype.Value{integer(1), integer(0)}
	dead := rowVersion{row: 1, version: 1, born: 1, values: []sqltype.Value{integer(99), integer(99)}}
	live := rowVersion{row: 2, version: 2, born: 3, values: oldValues}
	store := &relationStorage{versions: []rowVersion{dead, live}, ends: &deletionRoot{rows: map[ids.RowID]rowEnd{1: {version: 1, end: 2}}}}
	catalog.owner.catalog.state = catalog.state
	catalog.owner.commitSeq = 3
	catalog.owner.storage = map[ids.ObjectAddress]*relationStorage{leaf.address: store}
	snapshot, err := captureIndexSnapshot(context.Background(), catalog, catalog.owner.storage, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.sources) != 0 || len(snapshot.partitionRows) != 1 {
		t.Fatal("unindexed CHECK history was not captured", snapshot)
	}
	history := snapshot.partitionRows[leaf.address]
	if &history.store.versions[0] != &store.versions[0] {
		t.Fatal("original immutable versions were needlessly copied")
	}
	position := 0
	before, err := history.nextRow(context.Background(), &position, live)
	if err != nil || position != 2 || before[0] != oldValues[0] {
		t.Fatal("deleted source version changed old-row matching", position, before, err)
	}
	position = 0
	if _, err := history.nextRow(context.Background(), &position, rowVersion{row: 9, version: 9}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("mismatched source identity accepted", err)
	}
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: catalog}
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
	sourceColumn := leaf.checkConstraints[1].expression.(comparisonExpression).left.(columnExpression)
	virtualLeaf.generations = map[int32]columnGeneration{2: {kind: generationVirtual, expression: sourceColumn, planned: sourceColumn, dependencies: []ids.ObjectRef{sourceColumn.origin}}}
	virtualCheck := makeCheck(26002, "virtual_input", 1, sqltype.CompareEqual, 1)
	virtualExpression := virtualCheck.expression.(comparisonExpression)
	virtualColumn := virtualExpression.left.(columnExpression)
	virtualColumn = virtualColumn.withGeneration(&virtualLeaf, 1, 0)
	virtualExpression.left = virtualColumn
	virtualCheck.expression = virtualExpression
	virtualLeaf.checkConstraints = []checkConstraint{virtualCheck}
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
	literal := literalExpression{expressionInfo: expressionInfo{Usage: typ.Usage()}, value: integer(0)}
	oldCast := coercionExpression{expressionInfo: expressionInfo{Usage: oldDomain.Usage()}, child: literal, plan: oldPlan, mode: sqltype.Explicit}
	newCast := coercionExpression{expressionInfo: expressionInfo{Usage: newDomain.Usage()}, child: literal, plan: newPlan, mode: sqltype.Explicit}
	if affected, err := partitionCheckRecipeAffected(evaluation.Context, oldCast, newCast, false); err != nil || affected {
		t.Fatal("domain identity-only remap revalidated an old NOT VALID check", affected, err)
	}
	regclass, _ := catalog.LookupType(sqltype.Regclass)
	oldReference, err := regclass.Input(evaluation.Context, "42", -1)
	if err != nil {
		t.Fatal(err)
	}
	newReference, err := regclass.Input(evaluation.Context, "43", -1)
	if err != nil {
		t.Fatal(err)
	}
	beforeLiteral := literalExpression{expressionInfo: expressionInfo{Usage: regclass.Usage()}, value: oldReference}
	afterLiteral := literalExpression{expressionInfo: expressionInfo{Usage: regclass.Usage()}, value: newReference}
	if affected, err := partitionCheckRecipeAffected(evaluation.Context, beforeLiteral, afterLiteral, false); err != nil || !affected {
		t.Fatal("material reminted literal input was ignored", affected, err)
	}
	oidType, _ := catalog.LookupType(sqltype.Oid)
	ownerValue := tableOIDExpression{expressionInfo: expressionInfo{Usage: oidType.Usage()}, owner: catalog.owner.catalog.domain.Ref(catalog.Epoch(), leaf.address)}
	if affected, err := partitionCheckRecipeAffected(evaluation.Context, ownerValue, ownerValue, true); err != nil || !affected {
		t.Fatal("owner-sensitive recipe input was ignored", affected, err)
	}
	// An actual changed stored literal selects and enforces only its CHECK,
	// despite unchanged row cells and an unrelated old violating constraint.
	literalLeaf := *leaf
	literalLeaf.checkConstraints = append([]checkConstraint(nil), leaf.checkConstraints...)
	literalCheck := literalLeaf.checkConstraints[1].expression.(comparisonExpression)
	changedConstant := literalCheck.right.(literalExpression)
	changedConstant.value = integer(2)
	literalCheck.right = changedConstant
	literalLeaf.checkConstraints[1].expression = literalCheck
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
	destination := leaf.address
	destination.ObjectID = 27000
	snapshot.captureMapping(map[ids.ObjectAddress]ids.ObjectAddress{leaf.address: destination}, nil)
	if snapshot.partitionRows[destination].relation != leaf || snapshot.partitionRows[leaf.address].relation != nil {
		t.Fatal("row history address mapping lost provenance")
	}
}
