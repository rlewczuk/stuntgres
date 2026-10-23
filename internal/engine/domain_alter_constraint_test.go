package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Private seams only: parser/binder/catalog fixtures do not issue driver SQL.
func domainAlterUnitFence(t *testing.T, e *Engine, view catalog.Read, target ids.ObjectAddress) *typedef.DomainValidationFence {
	t.Helper()
	f, err := typedef.DomainUsesAt(view, target, []ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Uses {
		use := &f.Uses[i]
		use.Storage = e.storage.Relations[use.Relation.Address()]
		if use.Storage == nil {
			t.Fatal("missing physical use")
		}
		use.Versions = use.Storage.Versions[:len(use.Storage.Versions):len(use.Storage.Versions)]
		use.Ends, use.Generation = use.Storage.Ends, use.Storage.Generation
	}
	return f
}

func TestDomainAlterConstraintSelectedEvaluation(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.parent AS integer CHECK(FALSE)",
		"CREATE DOMAIN public.child AS public.parent CONSTRAINT a_error CHECK(1/0>0) CONSTRAINT b_selected CHECK(VALUE>0) CONSTRAINT c_unknown CHECK(NULL::boolean)",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	child := domainUnitType(t, view, "child")
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	sentinel := sqltype.Boolean(boolean, true)
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{DomainValue: sentinel, DomainFrameUsage: boolean.Usage(), DomainValueActive: true}}
	prior := &exec.Context{}
	evaluation.Input.DomainChecks = prior
	selected := *child
	selected.DomainChecks = []catalog.DomainCheckConstraint{child.DomainChecks[1]}
	if err := prepareOwnDomainChecks(&evaluation, &selected); err != nil {
		t.Fatal("selected preparation initialized unrelated recipe", err)
	}
	violation := errors.New("historical column violation")
	for _, n := range []int64{1, -1, 2} {
		value, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		err = evaluation.EvaluateSelectedDomainCheck(view, &selected, value, violation)
		if n < 0 && err != violation || n > 0 && err != nil {
			t.Fatal("selected truth was cached or ancestor evaluated", n, err)
		}
		if evaluation.DomainValue != sentinel || evaluation.DomainFrameUsage != boolean.Usage() || !evaluation.DomainValueActive || evaluation.Input.DomainChecks != prior || evaluation.RoutineRecipe || evaluation.RoutineGraft || evaluation.RoutineChecks != nil || evaluation.RoutineReferences != nil || evaluation.RoutineConsumer != nil {
			t.Fatal("VALUE/input/routine frame leaked")
		}
	}
	unknown := *child
	unknown.DomainChecks = []catalog.DomainCheckConstraint{child.DomainChecks[2]}
	if err := prepareOwnDomainChecks(&evaluation, &unknown); err != nil {
		t.Fatal(err)
	}
	if err := evaluation.EvaluateSelectedDomainCheck(view, &unknown, sqltype.Null(integer), violation); err != nil {
		t.Fatal("UNKNOWN did not pass", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation.Context = ctx
	if err := evaluation.EvaluateSelectedDomainCheck(view, &selected, sqltype.Null(integer), violation); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if evaluation.DomainValue != sentinel || evaluation.Input.DomainChecks != prior {
		t.Fatal("cancellation leaked frame")
	}
	leaf := plan.DomainValueExpr{Info: expr.Info{Usage: child.SQLType.DomainBase()}}
	evaluation.Context = context.Background()
	evaluation.DomainValue = sqltype.Null(child.SQLType.DomainBase().Type)
	evaluation.DomainFrameUsage = child.SQLType.DomainBase()
	if _, err := leaf.Eval(&evaluation); err != nil {
		t.Fatal("immediate domain base frame", err)
	}
}

func TestDomainAlterConstraintDiscovery(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE DOMAIN public.child AS public.d", "CREATE DOMAIN public.grandchild AS public.child", "CREATE TYPE public.unstored AS(v public.d)"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.direct(a public.d,b public.grandchild)")
	view := e.CatalogRead()
	d := domainUnitType(t, view, "d")
	f := domainAlterUnitFence(t, e, view, d.Address())
	if len(f.Uses) != 1 || f.Uses[0].Relation != table || !slices.Equal(f.Uses[0].Columns, []int{0, 1}) || !f.Current(view, e.storage.Relations) {
		t.Fatal("physical target/attribute deduplication", f.Uses)
	}
	domainUnitTable(t, e, op, "CREATE TABLE public.unrelated(v integer)")
	if !f.Current(e.CatalogRead(), e.storage.Relations) {
		t.Fatal("unrelated declaration invalidated observation")
	}
	domainUnitTable(t, e, op, "CREATE TABLE public.late(v public.child)")
	if f.Current(e.CatalogRead(), e.storage.Relations) {
		t.Fatal("late direct use escaped observation")
	}
	f = domainAlterUnitFence(t, e, e.CatalogRead(), d.Address())
	domainUnitTable(t, e, op, "CREATE TABLE public.blocker(v public.unstored[])")
	if f.Current(e.CatalogRead(), e.storage.Relations) {
		t.Fatal("late stored container escaped observation")
	}
	_, err := typedef.DomainUsesAt(e.CatalogRead(), d.Address(), []ids.ObjectAddress{})
	domainUnitDiagnostic(t, err, "0A000", `cannot alter type "public.d" because column "blocker.v" uses it`)
}

func TestDomainAlterConstraintGenerations(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE DOMAIN public.child AS public.d", "CREATE TYPE public.pair AS(v public.child)"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.stored(v public.child,a public.child[],c public.pair)")
	before := e.CatalogRead()
	d := domainUnitType(t, before, "d")
	child := domainUnitType(t, before, "child")
	value, err := child.SQLType.Input(context.Background(), "-1", -1)
	if err != nil {
		t.Fatal(err)
	}
	array := table.Columns[1].Usage.Type
	arrayValue, err := array.Input(context.Background(), "[0:1]={-1,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	composite, err := table.Columns[2].Usage.Type.Input(context.Background(), "(-1)", -1)
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Relation{Generation: 3, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value, arrayValue, composite}}}}
	e.mu.Lock()
	e.storage.InstallRelationLocked(table.Address(), store)
	e.mu.Unlock()
	e.visibility.SetCommitSeqLocked(3)
	// A permanently FALSE recipe proves pure retagging does not check old data.
	raw := statement(t, "CREATE DOMAIN public.unused AS integer CONSTRAINT positive CHECK(FALSE)").(*parser.CreateDomain)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	stage := e.BeginCatalogStage(auth.Catalog)
	next := *d
	if err == nil {
		err = e.bindDomainConstraints(op, auth, stage, &next, raw.Constraints, sqltype.InputEnvironment{})
	}
	if err == nil {
		next.DomainChecks[0].NotValid = true
		next.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), d.SQLType.SQLName(), d.SQLType.DomainBase(), true)
	}
	if err == nil {
		next.ObjectRevision, err = e.catalog.revisionLocked()
	}
	if err == nil {
		err = stage.Replace(&next)
	}
	stage.domainAlterState().Ctx = context.Background()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	current := e.CatalogRead()
	newD := domainUnitType(t, current, "d")
	newChild := domainUnitType(t, current, "child")
	if !newD.DomainChecks[0].NotValid || !newChild.SQLType.HasDomainChecks() || newD.SQLType.ID() != d.SQLType.ID() || newChild.SQLType.ID() != child.SQLType.ID() || newChild.SQLType.DomainBase().Type != newD.SQLType {
		t.Fatal("same-ID checked ancestry lost")
	}
	mapped := e.storage.Relations[table.Address()]
	if mapped == store || mapped.Versions[0].Values[0].Type() != newChild.SQLType || store.Versions[0].Values[0].Type() != child.SQLType || d.SQLType.HasDomainChecks() {
		t.Fatal("old row/type roots mutated")
	}
	mappedArray := mapped.Versions[0].Values[1]
	if mappedArray.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 0, Length: 2}) || !mappedArray.ArrayElement(1).IsNull() || mappedArray.ArrayElement(0).Type() != newChild.SQLType || mapped.Versions[0].Values[2].CompositeField(0).Type() != newChild.SQLType {
		t.Fatal("container generations/bounds/NULLs lost")
	}
	drop := e.BeginCatalogStage(current)
	if err := drop.removeDomainCheck(newD, newD.DomainChecks[0].OID); err != nil {
		t.Fatal(err)
	}
	drop.domainAlterState().Ctx = context.Background()
	domainUnitPublish(t, e, drop)
	if domainUnitType(t, e.CatalogRead(), "child").SQLType.HasDomainChecks() || !newChild.SQLType.HasDomainChecks() || !newD.DomainChecks[0].NotValid {
		t.Fatal("last DROP changed retained generations")
	}
	t.Run("index_artifacts", domainAlterGenerationIndexes)
}

func TestDomainAlterConstraintReplayEligibility(t *testing.T) {
	t.Run("fresh_owner_pair", domainAlterReplayOwnership)
	for _, tc := range []struct {
		sql   string
		valid bool
	}{
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", true},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK(VALUE>0)", true},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK(VALUE>0) NO INHERIT NOT VALID", false},
		{"ALTER DOMAIN d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", false},
		{"ALTER DOMAIN pg_temp.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", false},
		{"ALTER DOMAIN public.d ADD CHECK(VALUE>0) NOT VALID", false},
		{"ALTER DOMAIN public.d DROP CONSTRAINT positive", false},
		{"ALTER DOMAIN public.d VALIDATE CONSTRAINT positive", false},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK($1>0) NOT VALID", false},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK($1>0)", false},
	} {
		raw := statement(t, tc.sql).(*parser.AlterDomain)
		if err := restoreDomainAlter(raw); (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE DOMAIN public.provider AS public.d"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	d := domainUnitType(t, view, "d")
	provider := domainUnitType(t, view, "provider")
	recipeStage := domainUnitStage(t, e, op, "CREATE DOMAIN public.recipe AS integer CONSTRAINT late CHECK(VALUE::public.provider>0)")
	defer e.DiscardCatalogStage(recipeStage)
	var check catalog.DomainCheckConstraint
	for _, descriptor := range recipeStage.Descriptors() {
		if domain, ok := descriptor.(*catalog.Type); ok && domain.SQLType.IsDomain() {
			check = domain.DomainChecks[0]
		}
	}
	check.NotValid = true
	copy := *d
	copy.DomainChecks = []catalog.DomainCheckConstraint{check}
	graph := dump.Graph{{OID: d.Address().ObjectID}: nil, {OID: provider.Address().ObjectID}: {{OID: d.Address().ObjectID}}, {OID: 50002, Phase: dump.Rows}: {{OID: 50002}}, {OID: 50002}: nil, {OID: 50002, Phase: dump.TableCompletion}: {{OID: 50002, Phase: dump.Rows}}}
	if err := regobj.DomainInvalidDumpEvents(context.Background(), control.Resolver{}, view, graph, map[ids.OID]*catalog.Type{d.Address().ObjectID: &copy}); err != nil {
		t.Fatal(err)
	}
	late := dump.Event{OID: d.Address().ObjectID, Phase: dump.DomainChecks}
	if !slices.Contains(graph[late], dump.Event{OID: provider.Address().ObjectID}) || !slices.Contains(graph[late], dump.Event{OID: 50002, Phase: dump.TableCompletion}) || len(graph[dump.Event{OID: d.Address().ObjectID}]) != 0 {
		t.Fatal("post-data providers leaked into CREATE", graph)
	}
	if _, err := dump.Order(context.Background(), graph, nil); err != nil {
		t.Fatal("invented CREATE cycle", err)
	}
	graph[dump.Event{OID: provider.Address().ObjectID}] = append(graph[dump.Event{OID: provider.Address().ObjectID}], late)
	if _, err := dump.Order(context.Background(), graph, nil); err == nil {
		t.Fatal("genuine unresolved cycle accepted")
	}
}
