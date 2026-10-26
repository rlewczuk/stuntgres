package exec

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Eligibility belongs to this owner, not to an exported inspection API. The
// external tests exercise real folding/backing and the composed publisher;
// these owner-local assertions cover the private flag even for empty groups.
func TestPreparedFoldDefinitionEligibility(t *testing.T) {
	for _, name := range []string{"empty", "populated", "stored_only"} {
		t.Run(name, func(t *testing.T) {
			d := NewFoldDefinition()
			if !d.live || d.valid || d.all || d.generation != 0 {
				t.Fatal("new definition is reuse-eligible")
			}
			first := d.Begin()
			if name != "empty" {
				first.Candidate.Entries = []FoldConstant{{}}
				first.Candidate.Private = true
				if name == "stored_only" {
					first.Candidate.Entries[0].Origin.Owner = new(ids.Domain).Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1})
				}
			}
			accepted, err := d.Accept(&first)
			if err != nil || !d.valid || d.all || !first.Candidate.Sealed {
				t.Fatal("acceptance did not enable reuse", err)
			}
			stable := d.Begin()
			if _, err := d.Accept(&stable); err != nil || !d.valid || d.generation != accepted.Generation {
				t.Fatal("unchanged acceptance lost eligibility/generation", err)
			}
			stale := d.Begin()
			d.Invalidate(false)
			generation := d.generation
			if d.valid || d.all || generation != accepted.Generation+1 {
				t.Fatal("root retirement did not revoke eligibility")
			}
			if _, err := d.Accept(&stale); err != ids.ErrCatalogReplaced || d.valid {
				t.Fatal("rejected publication restored eligibility", err)
			}
			// Begin/failed planning alone cannot restore eligibility or overwrite
			// the previously accepted immutable group.
			retry := d.Begin()
			wantEntries := 0
			if name == "stored_only" {
				wantEntries = 1
				if &retry.Candidate.Entries[0] != &accepted.Entries[0] {
					t.Fatal("root retirement copied independently reusable stored backing")
				}
			}
			if d.valid || len(retry.Candidate.Entries) != wantEntries || len(d.entries) != len(accepted.Entries) {
				t.Fatal("begin resurrected retired roots or mutated accepted entries")
			}
			d.Invalidate(false)
			if d.valid || d.generation != generation {
				t.Fatal("repeated retirement changed eligibility/generation")
			}
			d.Invalidate(true)
			if d.valid || !d.all || d.generation != generation+1 {
				t.Fatal("all-origin escalation lost retirement")
			}
			repair := d.Begin()
			if _, err := d.Accept(&repair); err != nil || !d.valid || d.all {
				t.Fatal("successful repair did not restore eligibility", err)
			}
			d.Retire()
			if d.live || d.valid {
				t.Fatal("retired definition kept reuse/publication rights")
			}
			retired := d.Begin()
			if _, err := d.Accept(&retired); err != ids.ErrCatalogReplaced || d.valid {
				t.Fatal("retired definition regained eligibility", err)
			}
		})
	}
}

// Private input tags belong to exec. External owners compare public consumed
// fields and exercise actual publication/backing; no tag accessor is needed.
func TestPreparedFoldInputRepresentation(t *testing.T) {
	var typ *sqltype.Descriptor
	for _, builtin := range sqltype.Builtins() {
		if builtin.ID() == sqltype.Int4 {
			typ = builtin
			break
		}
	}
	if typ == nil {
		t.Fatal("missing integer descriptor")
	}
	usage := typ.Usage()
	value, err := sqltype.Integer(typ, 7)
	if err != nil {
		t.Fatal(err)
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}
	conversion, err := sqltype.PlanCoercion(usage, usage, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	node := plan.CoercionExpr{Info: expr.Info{Usage: usage}, Child: literal, Plan: conversion}
	provider := foldInput(node)
	omitted := foldInput(nil)
	null := foldInput(plan.LiteralExpr{Info: literal.Info, Value: sqltype.Null(typ)})
	invalid := foldInput(plan.LiteralExpr{Info: literal.Info})
	if !provider.provider || provider.Usage != usage || provider.Value.Type() != nil || omitted != (FoldInput{}) || null.provider || invalid.provider {
		t.Fatal("provider/literal/omitted representation changed")
	}
	for _, input := range []FoldInput{provider, omitted, null, foldInput(literal)} {
		if !sameFoldInput(input, input) {
			t.Fatal("valid input not reflexive")
		}
	}
	if sameFoldInput(provider, invalid) || sameFoldInput(provider, omitted) || sameFoldInput(omitted, null) || sameFoldInput(invalid, invalid) {
		t.Fatal("provider/omitted/typed NULL/invalid literal aliased")
	}
	// Selected wrappers retain both the private provider tag and its actual
	// literal operands. A literal with identical public fields is not a provider.
	selected := plan.SelectedOperatorExpr{Info: node.Info, Provider: node}
	var inputs []FoldInput
	foldExpressionInputs(selected, func(child expr.Expr) { inputs = append(inputs, foldInput(child)) })
	if len(inputs) != 2 || !inputs[0].provider || inputs[1].provider || !sameFoldInputs(inputs, selected) {
		t.Fatal("selected provider inputs lost their ordered representation")
	}
	changedInputs := append([]FoldInput(nil), inputs...)
	changedInputs[0].provider = false
	if sameFoldInputs(changedInputs, selected) {
		t.Fatal("literal representation inherited provider hit")
	}
	providerOwner := NewFoldDefinition()
	providerAttempt := providerOwner.Begin()
	providerContext := &Context{EvalStatement: EvalStatement{Context: context.Background(), FoldCaptures: &providerAttempt.Candidate}}
	if got, err := FoldPlanningExpression(providerContext, selected); err != nil || !sqltype.SameDatum(got, value) {
		t.Fatal("selected provider fold", got, err)
	}
	providerAccepted, err := providerOwner.Accept(&providerAttempt)
	if err != nil || len(providerAccepted.Entries) != 1 || len(providerAccepted.Entries[0].Inputs) != 2 || !providerAccepted.Entries[0].Inputs[0].provider {
		t.Fatal("selected provider publication lost private representation", err)
	}
	providerSource := append([]FoldInput(nil), providerAccepted.Entries[0].Inputs...)
	providerFixed := providerAccepted.Candidate()
	providerContext.FoldCaptures = &providerFixed
	if _, err := FoldPlanningExpression(providerContext, selected); err != nil || providerFixed.Private || &providerFixed.Entries[0] != &providerAccepted.Entries[0] {
		t.Fatal("selected provider missed immutable backing", err)
	}
	changedProvider := node
	changedProvider.Child = plan.LiteralExpr{Info: literal.Info, Value: sqltype.Null(typ)}
	selected.Provider = changedProvider
	if _, err := FoldPlanningExpression(providerContext, selected); err != ids.ErrCatalogReplaced {
		t.Fatal("selected provider hid changed physical operand", err)
	}
	for i, input := range providerSource {
		if providerAccepted.Entries[0].Inputs[i] != input {
			t.Fatal("provider hit/miss changed private source representation", i)
		}
	}
	// Exercise the real demanded boundary, fixed miss, and changed COW attempt,
	// not merely the local comparison predicates.
	owner := NewFoldDefinition()
	first := owner.Begin()
	ctx := &Context{EvalStatement: EvalStatement{Context: context.Background(), FoldCaptures: &first.Candidate}}
	if got, err := FoldPlanningExpression(ctx, node); err != nil || !sqltype.SameDatum(got, value) {
		t.Fatal("initial literal fold", got, err)
	}
	accepted, err := owner.Accept(&first)
	if err != nil || len(accepted.Entries) != 1 {
		t.Fatal("initial literal publication", err)
	}
	fixed := accepted.Candidate()
	ctx.FoldCaptures = &fixed
	if got, err := FoldPlanningExpression(ctx, node); err != nil || !sqltype.SameDatum(got, value) || fixed.Private || &fixed.Entries[0] != &accepted.Entries[0] {
		t.Fatal("identical literal missed accepted backing", err)
	}
	for _, child := range []plan.LiteralExpr{
		{Info: literal.Info},
		{Info: literal.Info, Value: sqltype.Null(typ)},
		{Info: expr.Info{Usage: usage, UnknownNull: true}, Value: value},
	} {
		node.Child = child
		if _, err := FoldPlanningExpression(ctx, node); err != ids.ErrCatalogReplaced {
			t.Fatal("incompatible literal inherited fixed artifact", err)
		}
		if fixed.Private || &fixed.Entries[0] != &accepted.Entries[0] || !sqltype.SameDatum(accepted.Entries[0].Inputs[0].Value, value) {
			t.Fatal("fixed miss mutated accepted input/backing")
		}
	}
	next := owner.Begin()
	ctx.FoldCaptures = &next.Candidate
	node.Child = plan.LiteralExpr{Info: literal.Info, Value: sqltype.Null(typ)}
	if got, err := FoldPlanningExpression(ctx, node); err != nil || !got.IsNull() || !next.Candidate.Private || &next.Candidate.Entries[0] == &accepted.Entries[0] {
		t.Fatal("changed literal did not use COW", got, err)
	}
	if !sqltype.SameDatum(accepted.Entries[0].Value, value) || !sqltype.SameDatum(accepted.Entries[0].Inputs[0].Value, value) || owner.Current().Generation != accepted.Generation {
		t.Fatal("private COW changed accepted source/generation")
	}
}
