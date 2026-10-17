package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestUserOperatorCatalogEstimatorVariants(t *testing.T) {
	name := operatorUnitName("public", "join_estimator")
	// Lookup-only inputs deliberately do not install procedures or providers.
	four := routineIdentity{id: 90000, namespace: PublicNamespaceOID, name: "join_estimator", signature: "2281,26,2281,21"}
	five := routineIdentity{id: 90001, namespace: PublicNamespaceOID, name: "join_estimator", signature: "2281,26,2281,21,2281"}
	for _, tc := range []struct {
		name                 string
		fourCount, fiveCount int
		want                 OID
		count                int
		code, message        string
	}{
		{name: "four_only", fourCount: 1, want: 90000, count: 1},
		{name: "five_only", fiveCount: 1, want: 90001, count: 1},
		{name: "neither"},
		{name: "ambiguous", fourCount: 1, fiveCount: 1, code: "42725", message: "join estimator function public.join_estimator has multiple matches"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, count, err := operatorEstimatorVariant(name, five, tc.fiveCount, four, tc.fourCount)
			if tc.code != "" {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message {
					t.Fatal(got, count, err)
				}
				return
			}
			if err != nil || count != tc.count || count != 0 && got.id != tc.want {
				t.Fatal(got, count, err)
			}
		})
	}
}

func TestUserOperatorCatalogEstimatorMetadata(t *testing.T) {
	name := operatorUnitName("public", "estimator")
	for _, tc := range []struct {
		name               string
		join               bool
		result             OID
		builtin, superuser bool
		code, message      string
	}{
		{name: "released_restriction", result: 701, builtin: true},
		{name: "released_join", join: true, result: 701, builtin: true},
		{name: "custom_superuser", result: 701, superuser: true},
		{name: "custom_join_superuser", join: true, result: 701, superuser: true},
		{name: "custom_privilege", result: 701, code: "42501", message: "must be superuser to specify a non-built-in selectivity estimation function"},
		{name: "custom_join_privilege", join: true, result: 701, code: "42501", message: "must be superuser to specify a non-built-in selectivity estimation function"},
		{name: "restriction_wrong_return", result: 23, builtin: true, code: "42P17", message: "restriction estimator function public.estimator must return type float8"},
		{name: "join_wrong_return", join: true, result: 23, builtin: true, code: "42P17", message: "join estimator function public.estimator must return type float8"},
		{name: "custom_wrong_return", result: 23, superuser: true, code: "42P17", message: "restriction estimator function public.estimator must return type float8"},
		{name: "missing_metadata", builtin: true, code: "0A000", message: "operator estimator return metadata is unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOperatorEstimatorMetadata(name, tc.join, tc.result, tc.builtin, tc.superuser)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message {
				t.Fatal(err)
			}
		})
	}
}

func TestUserOperatorCatalogEstimatorReleasedIdentity(t *testing.T) {
	e := New()
	_, auth := conversionUnitAuth(t, e)
	for _, tc := range []struct {
		name string
		join bool
		want OID
	}{{"eqsel", false, 101}, {"eqjoinsel", true, 105}} {
		id, err := e.operatorEstimator(auth, operatorUnitName("pg_catalog", tc.name), tc.join)
		if err != nil || id != tc.want || releasedOperatorEstimatorResult(id) != 701 {
			t.Fatal(id, err)
		}
		if _, callable := auth.catalog.state.descriptors[ObjectAddress{ClassID: ProcedureClassID, ObjectID: id}]; callable {
			t.Fatal("estimator identity fabricated a callable declaration", id)
		}
	}
}
