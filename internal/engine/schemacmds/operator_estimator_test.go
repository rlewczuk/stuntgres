package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestUserOperatorCatalogEstimatorReleasedIdentity(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	create := operatorUnitDefinition("#=#", "int4eq")
	create.Options = append(create.Options, operatorParsedOptions(t, "restrict=pg_catalog.eqsel,join=pg_catalog.eqjoinsel")...)
	stage := operatorUnitStage(t, e, op, auth, create)
	defer e.DiscardCatalogStage(stage)
	d := operatorUnitLookup(t, e.StageRead(stage), "#=#")
	for _, tc := range []struct {
		name     string
		id, want ids.OID
	}{{"eqsel", d.Restriction, 101}, {"eqjoinsel", d.Join, 105}} {
		if tc.id != tc.want || routine.ReleasedOperatorEstimatorResult(tc.id) != 701 {
			t.Fatal(tc.name, tc.id)
		}
		p, ok := auth.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.id})
		if !ok || p.Builtin == nil || p.Builtin != routine.BuiltinRoutine(tc.want) || p.Builtin.Providers != 0 ||
			p.Name() != tc.name || p.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) ||
			p.Result != 701 || p.RoutineKind != catalog.FunctionRoutine || !p.Strict || p.Volatility != expr.Stable ||
			p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: tc.name}) ||
			p.Builtin.Parallel != 's' || p.ReturnsSet() || p.CostEstimate() != 1 || p.SQL != nil || p.SQLBody != nil {
			t.Fatal("estimator identity lacks genuine noncallable metadata", tc.id, p)
		}
	}
}
