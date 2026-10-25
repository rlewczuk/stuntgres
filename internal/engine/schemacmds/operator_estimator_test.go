package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine"
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
		if _, callable := auth.Catalog.Find(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.id}); callable {
			t.Fatal("estimator identity fabricated a callable declaration", tc.id)
		}
	}
}
