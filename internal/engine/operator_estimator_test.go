package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestUserOperatorCatalogEstimatorReleasedIdentity(t *testing.T) {
	e := New()
	_, auth := conversionUnitAuth(t, e)
	for _, tc := range []struct {
		name string
		join bool
		want ids.OID
	}{{"eqsel", false, 101}, {"eqjoinsel", true, 105}} {
		id, err := e.operatorEstimator(auth, operatorUnitName("pg_catalog", tc.name), tc.join)
		if err != nil || id != tc.want || routine.ReleasedOperatorEstimatorResult(id) != 701 {
			t.Fatal(id, err)
		}
		if _, callable := auth.Catalog.Find(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); callable {
			t.Fatal("estimator identity fabricated a callable declaration", id)
		}
	}
}
