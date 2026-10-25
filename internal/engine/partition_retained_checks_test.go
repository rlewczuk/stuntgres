package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
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
		column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: slot}
		literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: integer(n)}
		predicate, err := sqltype.BindComparisonPredicate(operator, typ, typ, boolean, sqltype.CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		expression := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Left: column, Right: literal, Predicate: predicate}
		return catalog.CheckConstraint{OID: oid, Name: name, NotValid: true, Expression: expression, Dependencies: []catalog.ExpressionDependency{{Ref: ref}}}
	}
	leaf.Constraints.Check = []catalog.CheckConstraint{makeCheck(26000, "old_payload", 1, sqltype.CompareNotEqual, 0), makeCheck(26001, "changed_key", 0, sqltype.CompareEqual, 1)}
	oldValues := []sqltype.Value{integer(1), integer(0)}
	dead := storage.RowVersion{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{integer(99), integer(99)}}
	live := storage.RowVersion{Row: 2, Version: 2, Born: 3, Values: oldValues}
	store := &storage.Relation{Versions: []storage.RowVersion{dead, live}, Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 1, End: 2}}}}
	e.Catalog.PublishLocked(view.State())
	e.Visibility.SetCommitSeqLocked(3)
	e.Mu.Lock()
	e.Storage.ReplaceRelationsLocked(map[ids.ObjectAddress]*storage.Relation{leaf.Address(): store})
	e.Mu.Unlock()
	snapshot, err := EngineCaptureIndexSnapshot(e, context.Background(), view, e.Storage.Relations, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sources) != 0 || len(snapshot.PartitionRows) != 1 {
		t.Fatal("unindexed CHECK history was not captured", snapshot)
	}
	history := snapshot.PartitionRows[leaf.Address()]
	if &history.Store.Versions[0] != &store.Versions[0] {
		t.Fatal("original immutable versions were needlessly copied")
	}
	position := 0
	before, err := history.NextRow(context.Background(), &position, live)
	if err != nil || position != 2 || before[0] != oldValues[0] {
		t.Fatal("deleted source version changed old-row matching", position, before, err)
	}
	position = 0
	if _, err := history.NextRow(context.Background(), &position, storage.RowVersion{Row: 9, Version: 9}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("mismatched source identity accepted", err)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	var policy partition.RetainedChecks
	validate := func(values []sqltype.Value, want string) {
		t.Helper()
		d, err := policy.ForRow(evaluation.Context, history, leaf, before, values, snapshot, &evaluation)
		if err != nil {
			t.Fatal(err)
		}
		err = retainedRowError(d, values, &evaluation)
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
	err = retainedRowError(leaf, oldValues, &evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Constraint != "old_payload" {
		t.Fatal("full destination validation lost NOT VALID enforcement", err)
	}
	// A virtual generated CHECK input has no stored payload; its stored
	// dependency cells still determine whether this old constraint is affected.
	virtualLeaf := *leaf
	sourceColumn := leaf.Constraints.Check[1].Expression.(plan.ComparisonExpr).Left.(plan.ColumnExpr)
	virtualLeaf.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: sourceColumn, Planned: sourceColumn, Dependencies: []ids.ObjectRef{sourceColumn.Origin}}}
	virtualCheck := makeCheck(26002, "virtual_input", 1, sqltype.CompareEqual, 1)
	virtualExpression := virtualCheck.Expression.(plan.ComparisonExpr)
	virtualColumn := virtualExpression.Left.(plan.ColumnExpr)
	virtualColumn = virtualColumn.WithGeneration(&virtualLeaf, 1, 0)
	virtualExpression.Left = virtualColumn
	virtualCheck.Expression = virtualExpression
	virtualLeaf.Constraints.Check = []catalog.CheckConstraint{virtualCheck}
	virtualHistory := history
	virtualHistory.Relation = &virtualLeaf
	virtualBefore := []sqltype.Value{integer(1), sqltype.Null(typ)}
	virtualAfter := []sqltype.Value{integer(2), sqltype.Null(typ)}
	var virtualPolicy partition.RetainedChecks
	virtualDescriptor, err := virtualPolicy.ForRow(evaluation.Context, virtualHistory, &virtualLeaf, virtualBefore, virtualAfter, snapshot, &evaluation)
	if err != nil {
		t.Fatal(err)
	}
	err = retainedRowError(virtualDescriptor, virtualAfter, &evaluation)
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
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: integer(0)}
	oldCast := plan.CoercionExpr{Info: expr.Info{Usage: oldDomain.Usage()}, Child: literal, Plan: oldPlan, Mode: sqltype.Explicit}
	newCast := plan.CoercionExpr{Info: expr.Info{Usage: newDomain.Usage()}, Child: literal, Plan: newPlan, Mode: sqltype.Explicit}
	if affected, err := partition.CheckRecipeAffected(evaluation.Context, oldCast, newCast, false); err != nil || affected {
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
	beforeLiteral := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: oldReference}
	afterLiteral := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: newReference}
	if affected, err := partition.CheckRecipeAffected(evaluation.Context, beforeLiteral, afterLiteral, false); err != nil || !affected {
		t.Fatal("material reminted literal input was ignored", affected, err)
	}
	oidType, _ := view.LookupType(sqltype.Oid)
	ownerValue := plan.TableOIDExpr{Info: expr.Info{Usage: oidType.Usage()}, Owner: view.Owner().Domain.Ref(view.Epoch(), leaf.Address())}
	if affected, err := partition.CheckRecipeAffected(evaluation.Context, ownerValue, ownerValue, true); err != nil || !affected {
		t.Fatal("owner-sensitive recipe input was ignored", affected, err)
	}
	// An actual changed stored literal selects and enforces only its CHECK,
	// despite unchanged row cells and an unrelated old violating constraint.
	literalLeaf := *leaf
	literalLeaf.Constraints.Check = append([]catalog.CheckConstraint(nil), leaf.Constraints.Check...)
	literalCheck := literalLeaf.Constraints.Check[1].Expression.(plan.ComparisonExpr)
	changedConstant := literalCheck.Right.(plan.LiteralExpr)
	changedConstant.Value = integer(2)
	literalCheck.Right = changedConstant
	literalLeaf.Constraints.Check[1].Expression = literalCheck
	var literalPolicy partition.RetainedChecks
	literalDescriptor, err := literalPolicy.ForRow(evaluation.Context, history, &literalLeaf, oldValues, oldValues, snapshot, &evaluation)
	if err != nil {
		t.Fatal(err)
	}
	err = retainedRowError(literalDescriptor, oldValues, &evaluation)
	if !errors.As(err, &diagnostic) || diagnostic.Constraint != "changed_key" {
		t.Fatal("changed stored literal did not enforce its CHECK", err)
	}
	// Mapping changes the candidate lookup key, not its immutable original row authority.
	destination := leaf.Address()
	destination.ObjectID = 27000
	snapshot.CaptureMapping(map[ids.ObjectAddress]ids.ObjectAddress{leaf.Address(): destination}, nil)
	if snapshot.PartitionRows[destination].Relation != leaf || snapshot.PartitionRows[leaf.Address()].Relation != nil {
		t.Fatal("row history address mapping lost provenance")
	}
}

// retainedRowError renders a physical row violation with a fixed detail.
func retainedRowError(relation *catalog.Relation, row []sqltype.Value, evaluation *exec.Context) error {
	violation, err := table.PhysicalRowViolation(relation, row, evaluation)
	if err != nil || violation == nil {
		return err
	}
	return violation.Diagnostic("public", "test row")
}
