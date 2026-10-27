package exec

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRoutineAttributeDescriptorIdentity(t *testing.T) {
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90001}, Strict: true, Volatility: expr.Immutable, SecurityDefiner: true, Parallel: 'r', Cost: 7, Rows: 12, Configuration: catalog.NewRoutineConfiguration([]string{"work_mem=64kB"})}
	copy := *d
	if !SameRoutineDeclaration(d, &copy) {
		t.Fatal("unchanged immutable declaration rejected")
	}
	for _, change := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Volatility = expr.Stable },
		func(p *catalog.Procedure) { p.SecurityDefiner = false }, func(p *catalog.Procedure) { p.Parallel = 's' },
		func(p *catalog.Procedure) { p.Cost = 8 }, func(p *catalog.Procedure) { p.Rows = 13 },
		func(p *catalog.Procedure) {
			p.Configuration = catalog.NewRoutineConfiguration([]string{"work_mem=64kB"})
		},
	} {
		copy := *d
		change(&copy)
		if SameRoutineDeclaration(d, &copy) {
			t.Fatal("stale semantic carrier accepted", copy)
		}
	}
	if d.Configuration.At(0) != "work_mem=64kB" {
		t.Fatal("old declaration changed")
	}
}
