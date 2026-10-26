package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// This supplements the composed definition/publication tests: every candidate
// here is also a real input to the demanded production folding boundary.
func TestPreparedFoldDefinitionStateMachine(t *testing.T) {
	e := engine.New()
	value := preparedFoldFloatWord(t, e, sqltype.Float8, 0x7ff8000000000001)
	usage := value.Type().Usage()
	conversion, err := sqltype.PlanCoercion(usage, usage, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	node := plan.CoercionExpr{Info: expr.Info{Usage: usage}, Child: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}, Plan: conversion}
	origin := exec.FoldOrigin{Owner: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Float8)})}
	fold := func(group *exec.FoldCaptureCandidate, stored bool) {
		t.Helper()
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead(), FoldCaptures: group}}
		if stored {
			ctx.FoldOrigin = origin
		}
		if got, err := exec.FoldPlanningExpression(ctx, node); err != nil || !sqltype.SameDatum(got, value) {
			t.Fatal("real fold failed", err)
		}
	}
	d := exec.NewFoldDefinition()
	if !d.Live() || d.Current().Generation != 0 || len(d.Begin().Candidate.Entries) != 0 {
		t.Fatal("new definition has an accepted generation")
	}
	first := d.Begin()
	fold(&first.Candidate, false)
	fold(&first.Candidate, true)
	accepted, err := d.Accept(&first)
	if err != nil || !first.Candidate.Sealed || accepted.Generation != 1 || len(accepted.Entries) != 2 || len(accepted.Dependencies) == 0 {
		t.Fatal("complete first publication missing", err)
	}
	backing, dependency := &accepted.Entries[0], &accepted.Dependencies[0]
	if _, err := d.Accept(&first); err != ids.ErrCatalogReplaced {
		t.Fatal("sealed attempt accepted twice", err)
	}
	stable := d.Begin()
	if &stable.Candidate.Entries[0] != backing || &stable.Candidate.Dependencies[0] != dependency {
		t.Fatal("Begin copied immutable resources")
	}
	fold(&stable.Candidate, false)
	fold(&stable.Candidate, true)
	if stable.Candidate.Private || stable.Candidate.DependenciesPrivate {
		t.Fatal("stable hit made a private copy")
	}
	unchanged, err := d.Accept(&stable)
	if err != nil || unchanged.Generation != accepted.Generation || &unchanged.Entries[0] != backing || &unchanged.Dependencies[0] != dependency {
		t.Fatal("stable acceptance changed generation/backing", err)
	}
	foreign := exec.NewFoldDefinition()
	wrongOwner := d.Begin()
	if _, err := foreign.Accept(&wrongOwner); err != ids.ErrCatalogReplaced {
		t.Fatal("foreign definition accepted attempt", err)
	}
	stale := d.Begin()
	d.Invalidate(false)
	retiredGeneration := d.Current().Generation
	if retiredGeneration != accepted.Generation+1 {
		t.Fatal("root invalidation did not retire generation")
	}
	d.Invalidate(false)
	if d.Current().Generation != retiredGeneration {
		t.Fatal("repeated root invalidation advanced generation")
	}
	if _, err := d.Accept(&stale); err != ids.ErrCatalogReplaced {
		t.Fatal("stale generation published", err)
	}
	rootRefresh := d.Begin()
	if len(rootRefresh.Candidate.Entries) != 1 || rootRefresh.Candidate.Entries[0].Origin != origin || !rootRefresh.Candidate.Private {
		t.Fatal("root retirement did not preserve exact stored origin")
	}
	if len(accepted.Entries) != 2 || &accepted.Entries[0] != backing || accepted.Generation != 1 {
		t.Fatal("root retirement mutated old accepted group")
	}
	// An unaccepted (failed) refresh cannot resurrect root syntax or overwrite
	// the old immutable payload, even when the caller restores its environment.
	retry := d.Begin()
	if len(retry.Candidate.Entries) != 1 || retry.Candidate.Entries[0].Origin != origin {
		t.Fatal("failed refresh resurrected root entry")
	}
	fold(&retry.Candidate, false)
	refreshed, err := d.Accept(&retry)
	if err != nil || refreshed.Generation != retiredGeneration+1 || &refreshed.Entries[0] == backing {
		t.Fatal("changed refresh failed COW acceptance", err)
	}
	reused := d.Begin()
	if len(reused.Candidate.Entries) != len(refreshed.Entries) || &reused.Candidate.Entries[0] != &refreshed.Entries[0] {
		t.Fatal("accepted refresh did not restore real reuse eligibility")
	}
	if &accepted.Entries[0] != backing || accepted.Entries[0].Origin != (exec.FoldOrigin{}) {
		t.Fatal("changed publication modified old backing")
	}
	if _, err := d.Accept(&rootRefresh); err != ids.ErrCatalogReplaced {
		t.Fatal("overlapping refresh published stale candidate", err)
	}
	d.Invalidate(false)
	rootGeneration := d.Current().Generation
	d.Invalidate(true)
	allGeneration := d.Current().Generation
	if allGeneration != rootGeneration+1 {
		t.Fatal("all-origin escalation did not advance generation")
	}
	d.Invalidate(true)
	if d.Current().Generation != allGeneration {
		t.Fatal("repeated all-origin retirement advanced generation")
	}
	all := d.Begin()
	if len(all.Candidate.Entries) != 0 || len(all.Candidate.Dependencies) != 0 || !all.Candidate.Private || !all.Candidate.DependenciesPrivate {
		t.Fatal("all-origin retirement retained typed resources")
	}
	// A saved binding remains a fixed, nonpublishing real planning input even
	// after definition retirement. NaN identity must still hit without COW.
	d.Retire()
	if d.Live() {
		t.Fatal("retired definition remains live")
	}
	if _, err := d.Accept(&all); err != ids.ErrCatalogReplaced {
		t.Fatal("retired definition accepted candidate", err)
	}
	postRetire := d.Begin()
	if _, err := d.Accept(&postRetire); err != ids.ErrCatalogReplaced {
		t.Fatal("retired definition minted publication rights", err)
	}
	fixed := accepted.Candidate()
	if !fixed.Fixed || fixed.Publication != nil || &fixed.Entries[0] != backing || &fixed.Dependencies[0] != dependency {
		t.Fatal("binding candidate lost accepted backing or gained publication")
	}
	fold(&fixed, false)
	fold(&fixed, true)
	if fixed.Private || fixed.DependenciesPrivate || accepted.Generation != 1 {
		t.Fatal("accepted binding hit changed resources/generation")
	}
	node.Child = plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: preparedFoldFloatWord(t, e, sqltype.Float8, 0x7ff8000000000002)}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead(), FoldCaptures: &fixed}}
	if _, err := exec.FoldPlanningExpression(ctx, node); err != ids.ErrCatalogReplaced {
		t.Fatal("accepted binding recomputed changed physical input", err)
	}
	if &accepted.Entries[0] != backing || &accepted.Dependencies[0] != dependency || accepted.Generation != 1 {
		t.Fatal("rejected fixed miss changed old accepted group")
	}
}
