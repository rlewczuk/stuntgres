package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestStatisticsMCVItemsOwnership(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	// Parse/describe only: public SQL outcomes belong to reference/. This real
	// description combines a fixed source definition with an owned scalar fold.
	statement, description := enginetest.PreparedDescription(t, e, session, "SELECT COALESCE(-(7::integer),0) AS witness FROM pg_mcv_list_items(NULL::pg_mcv_list)")
	op, err := session.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.Describe(t.Context(), e.AnalysisHost(op), statement, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, &description, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := binding.Query.Root.(*plan.ProjectNode)
	source := root.Child.(*plan.FunctionFromNode)
	call := source.Calls[0].(functions.StatisticsMCVItems)
	info, child := call.Info, call.Arg
	declaration, err := exec.ExpressionRoutineDescriptor(call)
	if err != nil || declaration == nil || declaration.Address().ObjectID != 3427 || auth.Catalog.Get(declaration.Address()) != declaration || info.Occurrence == 0 || info.Source == (expr.FoldSourceOccurrence{}) {
		t.Fatal("source lacks actual definition/provenance authority", declaration, info, err)
	}
	publication, err := core.BeginFoldPublication(&description, statement, environment, auth.Catalog, nil)
	if err != nil || publication == nil {
		t.Fatal(publication, err)
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: auth.Catalog, FoldCaptures: publication.Group}}
	// Demand the conditional planner, which folds its immutable arithmetic
	// child before selecting COALESCE. Calling the scalar boundary on the
	// unsimplified parent alone would not produce a retained constant.
	if _, changed, err := exec.SimplifyConditional(evaluation, root.Expressions[0], false); err != nil || !changed {
		t.Fatal("demanded conditional fold", changed, err)
	}
	if len(publication.Group.Entries) == 0 {
		t.Fatal("composed description has no real owned fold witness")
	}
	if err := publication.Accept(t.Context(), auth.Catalog, environment, auth.Catalog.Epoch()); err != nil {
		t.Fatal(err)
	}
	accepted := description.Folds.Current()
	acceptedAnalysis := description.Analysis
	entry := &accepted.Entries[0]
	for _, folded := range accepted.Entries {
		if folded.Value.Type().ID() == sqltype.PgMCVList || folded.Value.Type().ID() == 2249 {
			t.Fatal("stable set source entered persistent scalar-fold backing")
		}
	}
	candidate, err := core.BeginFoldPublication(&description, statement, environment, auth.Catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.FoldCaptures = candidate.Group
	if _, changed, err := exec.SimplifyConditional(evaluation, root.Expressions[0], false); err != nil || !changed {
		t.Fatal("retained conditional fold", changed, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := candidate.Accept(canceled, auth.Catalog, environment, auth.Catalog.Epoch()); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled candidate accepted", err)
	}
	current := description.Folds.Current()
	if current.Generation != accepted.Generation || &current.Entries[0] != entry || description.Analysis != acceptedAnalysis || binding.Query.Root != root || root.Child != source || source.Calls[0].(functions.StatisticsMCVItems).Info != info || !reflect.DeepEqual(source.Calls[0].(functions.StatisticsMCVItems).Arg, child) {
		t.Fatal("rejected publication mutated accepted graph/generation/backing")
	}
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	unchanged, changed, err := stored.RetagTypes(auth.Catalog, call, identity)
	if err != nil || changed || !reflect.DeepEqual(unchanged, call) {
		t.Fatal("identity remap changed accepted provider", unchanged, changed, err)
	}
	// Equivalent immutable declarations need not be the same catalog payload
	// pointer. An identity rewrite must retain the original accepted carrier.
	equivalent := *declaration
	copied := call
	copied.RoutineReference = &equivalent
	retained, changed, err := stored.RetagTypes(auth.Catalog, copied, identity)
	if err != nil || changed || retained.(functions.StatisticsMCVItems).RoutineReference != &equivalent {
		t.Fatal("equivalent original declaration lost its backing", changed, err)
	}
	// A genuinely different catalog/type generation must rebuild the OUT shape
	// and formal without retaining the source's generation beside the remap.
	target := engine.New()
	destination := target.CatalogRead()
	mapped, changed, err := stored.RetagTypes(destination, call, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		mapped, _ := destination.LookupType(d.ID())
		return mapped
	})
	if err != nil || !changed {
		t.Fatal("destination remap", changed, err)
	}
	next := mapped.(functions.StatisticsMCVItems)
	if next.Usage.Type == call.Usage.Type || next.Arg.ResultUsage().Type == child.ResultUsage().Type || next.SameSourceOccurrence(call) || next.ValidateStoredProvider() != nil {
		t.Fatal("destination retained old generation", next)
	}
	for i := range 5 {
		oldMember, newMember := call.Usage.Type.CompositeMember(i), next.Usage.Type.CompositeMember(i)
		want, _ := destination.LookupType(oldMember.Usage.Type.ID())
		if newMember.Usage.Type != want || newMember.Usage.Type == oldMember.Usage.Type {
			t.Fatal("foreign destination OUT member", i, newMember)
		}
	}
	forged := call
	foreign := *declaration
	foreign.Display.Source = "pg_mcv_list_out"
	forged.RoutineReference = &foreign
	if _, _, err := stored.RetagTypes(auth.Catalog, forged, identity); err == nil {
		t.Fatal("forged original declaration acquired stored authority")
	}
	missing := call
	missing.RoutineReference = nil
	if _, _, err := stored.RetagTypes(auth.Catalog, missing, identity); err == nil {
		t.Fatal("missing original declaration acquired stored authority")
	}
	wrong, ok := auth.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 5021})
	if !ok {
		t.Fatal("missing genuine alternate builtin declaration")
	}
	forged.RoutineReference = wrong
	if _, _, err := stored.RetagTypes(auth.Catalog, forged, identity); err == nil {
		t.Fatal("wrong genuine declaration acquired MCV item authority")
	}
	foreign = *declaration
	foreign.Strict = !foreign.Strict
	forged.RoutineReference = &foreign
	if _, _, err := stored.RetagTypes(auth.Catalog, forged, identity); err == nil {
		t.Fatal("forged original declaration ABI acquired stored authority")
	}
	badInfo := call.Info
	badInfo.Usage = call.Arg.ResultUsage()
	if _, err := call.Rebuild(badInfo, call.Children()); err == nil {
		t.Fatal("invalid rebuilt shape accepted")
	}
	if call.Info != info || !reflect.DeepEqual(call.Arg, child) || description.Folds.Current().Generation != accepted.Generation || &description.Folds.Current().Entries[0] != entry {
		t.Fatal("failed reconstruction/remap altered original accepted ownership")
	}
}

func TestStatisticsMCVItemsStoredUsage(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	statement, description := enginetest.PreparedDescription(t, e, session, "SELECT m.* FROM pg_statistic_ext_data d CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE FALSE")
	op, err := session.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.Describe(t.Context(), e.AnalysisHost(op), statement, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, &description, nil)
	if err != nil {
		t.Fatal(err)
	}
	foreign := engine.New().CatalogRead()
	for _, id := range []sqltype.ID{10028, 10029} {
		typ, ok := auth.Catalog.LookupType(id)
		if !ok {
			t.Fatal("missing actual catalog carrier", id)
		}
		usage := typ.Usage()
		if usage.Valid() || !usage.CatalogNullUsage() || !stored.ValidExpressionUsage(auth.Catalog, usage) {
			t.Fatal("result-only carrier lost its bounded stored-query authority", usage)
		}
		copy := *typ
		wrongTypmod, wrongCollation := usage, usage
		wrongTypmod.Typmod = 0
		wrongCollation.Collation = sqltype.CollationRef{ID: 950}
		other, ok := foreign.LookupType(id)
		if !ok || other == typ {
			t.Fatal("missing independent catalog generation", id)
		}
		for _, bad := range []sqltype.Usage{
			copy.Usage(),
			sqltype.LookupOnly(id, typ.Name(), 0).Usage(),
			wrongTypmod,
			wrongCollation,
			other.Usage(),
		} {
			if stored.ValidExpressionUsage(auth.Catalog, bad) {
				t.Fatal("invalid result carrier acquired current stored authority", bad)
			}
		}
	}
	// Check the actual described lateral graph, not a fabricated provider
	// shape. Its full DATA source schema retains unprojected stxdexpr10028.
	carrier, provider := false, false
	check := func(usage sqltype.Usage) {
		t.Helper()
		if !stored.ValidExpressionUsage(auth.Catalog, usage) {
			t.Fatal("actual lateral graph has invalid stored usage", usage)
		}
		if usage.Type.ID() == 10028 {
			carrier = true
			if !usage.CatalogNullUsage() || usage.Valid() {
				t.Fatal("DATA schema manufactured ordinary type admission")
			}
		}
	}
	plan.VisitExecutableQuery(binding.Query.Root, func(node plan.Node) {
		for _, column := range node.Schema() {
			check(column.Usage)
		}
	}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			check(node.ResultUsage())
			if call, ok := node.(functions.StatisticsMCVItems); ok {
				provider = true
				if err := call.ValidateStoredProvider(); err != nil || call.Arg.ResultUsage().Type.ID() != sqltype.PgMCVList {
					t.Fatal("actual lateral source lost checked formal/OUT usage", err)
				}
			}
		})
	})
	if !carrier || !provider {
		t.Fatal("actual lateral ownership witnesses absent", carrier, provider)
	}
}
