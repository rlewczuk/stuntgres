package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCastImplementedAdmission(t *testing.T) {
	facts := castUnitUniverse(t)
	byName := map[string]castUnitFact{}
	for _, f := range facts {
		if _, ok := byName[f.name]; ok {
			t.Fatal("duplicate unit source", f.name)
		}
		byName[f.name] = f
	}
	edges := castUnitEdges()
	visited := 0
	for _, source := range facts {
		for _, target := range facts {
			for _, mode := range []CoercionContext{Implicit, Assignment, Explicit} {
				t.Run(castUnitLabel(source, target, mode), func(t *testing.T) {
					admitted, unavailable := castUnitExpected(source, target, mode, byName, edges)
					plan, err := PlanCoercion(source.typ.Usage(), target.typ.Usage(), mode)
					if admitted && !unavailable {
						if err != nil {
							t.Fatal("released kernel operation refused", err)
						}
						// Admission precedes strictness, but an admitted operation
						// still owns its destination NULL and domain entry checks.
						value, err := plan.Apply(context.Background(), Null(source.typ), InputEnvironment{DomainChecks: castUnitDomainRule{}}, OutputEnvironment{})
						if target.name == "cast_required" && source.name != target.name {
							castUnitError(t, err, "23502", "domain cast_required does not allow null values")
						} else if err != nil || !value.IsNull() || value.Type() != target.typ {
							t.Fatal("admitted NULL operation lost destination ownership", value, err)
						}
						return
					}
					if err == nil {
						t.Fatal("forbidden/unavailable kernel operation admitted")
					}
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) {
						t.Fatal("missing owned SQL diagnostic", err)
					}
					code := "42804"
					if unavailable {
						code = "0A000"
					}
					if diagnostic.Code != code {
						t.Fatalf("kernel classification=%s want %s: %v", diagnostic.Code, code, err)
					}
				})
				visited++
			}
		}
	}
	if visited != len(facts)*len(facts)*3 {
		t.Fatal("incomplete context Cartesian product", visited)
	}
	// Lookup identities are not executable occurrence shapes. No broad OID2249 or
	// companion-array exemption may turn these into valid ordinary kernel usages.
	for _, d := range Builtins() {
		if d.ID() == 2249 || d.ID() == 2287 {
			if d.Concrete() {
				t.Fatal("generic record catalog identity became concrete", d)
			}
			if _, err := PlanCoercion(d.Usage(), d.Usage(), Explicit); err == nil {
				t.Fatal("lookup-only record entered kernel")
			}
		}
	}
}
