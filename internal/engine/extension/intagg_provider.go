package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

type IntaggProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *IntaggProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *IntaggProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}}
}

var StockIntagg = &IntaggProvider{control: catalog.ExtensionControl{Version: "1.1", Superuser: true, Relocatable: true, Comment: new("integer aggregator and enumerator (obsolete)")}}

var IntaggMembers = [...]struct {
	Name       string
	Signature  catalog.ProcedureSignature
	Result     ids.OID
	Strict     bool
	Kind       catalog.RoutineKind
	Source     string
	Volatility expr.Volatility
}{
	{"int_agg_state", "2281,23", 2281, false, catalog.FunctionRoutine, "array_agg_transfn", expr.Volatile},
	{"int_agg_final_array", "2281", 1007, false, catalog.FunctionRoutine, "array_agg_finalfn", expr.Volatile},
	{"int_array_aggregate", "23", 1007, false, catalog.AggregateRoutine, "aggregate_dummy", expr.Immutable},
	{"int_array_enum", "1007", 23, true, catalog.FunctionRoutine, "array_unnest", expr.Immutable},
}

func (p *IntaggProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intagg manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockIntagg || v.Owner() == nil || ext.Name() != "intagg" || ext.Version != p.control.Version || len(ext.Members) != len(IntaggMembers) {
		return invalid()
	}
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *IntaggProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
