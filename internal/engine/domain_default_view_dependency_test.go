package engine

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func TestDomainDefaultViewSQLDependenciesInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.dd_view_domain AS integer DEFAULT 7",
		"CREATE TABLE public.dd_view_source(a public.dd_view_domain,b integer)",
		"CREATE VIEW public.dd_view_column AS SELECT a FROM public.dd_view_source",
		"CREATE VIEW public.dd_view_explicit AS SELECT a,NULL::public.dd_view_domain AS extra FROM public.dd_view_source",
		"CREATE VIEW public.dd_view_literal AS SELECT NULL::public.dd_view_domain AS extra",
	} {
		roleExecute(t, e, op, q)
	}
	catalogView := e.CatalogRead()
	domain := domainUnitType(t, catalogView, "dd_view_domain")
	stage := e.BeginCatalogStage(catalogView)
	defer e.DiscardCatalogStage(stage)
	provider := StageDropProvider(op.Context(), stage, nil, nil, nil)
	for _, tc := range []struct {
		name    string
		passive bool
	}{{"dd_view_column", true}, {"dd_view_explicit", false}, {"dd_view_literal", false}} {
		d := catalogView.MustRelation(roleTableRef(t, e, tc.name).Address)
		if !view.ViewDepends(d, domain.Address()) {
			t.Fatal("missing original lifetime capture", tc.name)
		}
		got, err := view.PassiveTypeDependency(provider, d, domain.Address())
		if err != nil || got != tc.passive {
			t.Fatal("SQL column/type edge classification", tc.name, got, err)
		}
		if !view.ViewDepends(d, domain.Address()) {
			t.Fatal("SQL classification pruned lifetime authority", tc.name)
		}
	}
}

func TestDomainDefaultDependencyPreparedProjectionInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE TABLE public.dd_projection_source(a integer,b integer)",
		"CREATE VIEW public.dd_projection_retired AS SELECT b FROM public.dd_projection_source",
		"CREATE VIEW public.dd_projection_alias AS SELECT 'public.dd_projection_retired'::regtype AS typ",
		"CREATE VIEW public.dd_projection_live_alias AS SELECT 'public.dd_projection_source'::regtype AS typ",
	} {
		roleExecute(t, e, op, q)
	}
	before := e.CatalogRead()
	sourceRef := roleTableRef(t, e, "dd_projection_source")
	source := before.MustRelation(sourceRef.Address)
	retired := before.MustRelation(roleTableRef(t, e, "dd_projection_retired").Address)
	retiredQuery := stored.ViewOf(retired).Query()
	query := stored.ViewOf(before.MustRelation(roleTableRef(t, e, "dd_projection_alias").Address)).Query()
	liveQuery := stored.ViewOf(before.MustRelation(roleTableRef(t, e, "dd_projection_live_alias").Address)).Query()
	target := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(retired.RowType)}
	refs, err := stored.QueryDependRefs(op.Context(), before, nil, query)
	if err != nil || !slices.Contains(refs, target) {
		t.Fatal("genuine alias did not retain its actual rowtype", refs, err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	raw := statement(t, "ALTER TABLE public.dd_projection_source DROP COLUMN b CASCADE").(*parser.AlterTable)
	if err := scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.AcquireSequence(sourceRef, lock.AccessExclusive, true); err != nil {
		t.Fatal(err)
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareColumnDrop(scope, auth, source, sourceRef, raw.Actions[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, present := prepared.Descriptor(target); present {
		t.Fatal("real prepared CASCADE retained retired view rowtype")
	}
	liveRefs, err := stored.QueryDependRefs(op.Context(), before, prepared, liveQuery)
	if err != nil || !slices.Contains(liveRefs, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}) {
		t.Fatal("prepared projection lost genuine surviving alias", liveRefs, err)
	}
	baseRefs, err := stored.QueryDependRefs(op.Context(), before, nil, query)
	if err != nil || !slices.Contains(baseRefs, target) {
		t.Fatal("prepared discovery lost the genuine predecessor alias", baseRefs, err)
	}
	retiredRefs, err := stored.QueryDependRefs(op.Context(), before, prepared, query)
	if err != nil || slices.Contains(retiredRefs, target) {
		t.Fatal("opaque retired alias revived a stale base dependency", retiredRefs, err)
	}
	// Opaque numeric aliases may outlive their targets, but a real bound
	// column cannot survive the same ordered prepared DROP projection.
	if _, err := stored.QueryDependRefs(op.Context(), before, prepared, retiredQuery); err == nil {
		t.Fatal("query collector accepted the genuinely retired source column")
	}
	if e.CatalogRead().State() != before.State() || before.Get(source.Address()) != source || before.Get(target) == nil {
		t.Fatal("read-only preparation published or mutated predecessor")
	}
}
