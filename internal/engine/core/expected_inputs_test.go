package core_test

import (
	"stuntgres/internal/engine/core"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/sqltype"
)

// A held description reaches the binder as an analyze value. A missing
// description is not held; a present one copies every input the binder reads.
func TestExpectedOfCopiesHeldInputs(t *testing.T) {
	if got := core.ExpectedOf(nil); !reflect.DeepEqual(got, analyze.Expected{}) {
		t.Fatalf("core.ExpectedOf(nil) = %+v", got)
	}
	owner := &catalog.Owner{}
	d := &core.Description{
		Parameters:       analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{{}}},
		Results:          []catalog.Column{{Name: "a"}},
		CatalogOwner:     owner,
		CatalogEpoch:     ids.CatalogEpoch(3),
		Temporals:        []analyze.TemporalConstant{{Offset: 4}},
		Types:            map[analyze.PreparedTypeKey]analyze.TypeOccurrence{{}: {}},
		Sequences:        &sequence.Analysis{Constants: []sequence.Constant{{Offset: 5}}},
		Regtypes:         &regobj.Analysis{Constants: []analyze.RegtypeConstant{{Offset: 6}}},
		PlanOwner:        ids.SessionToken{},
		PlanGeneration:   7,
		RefreshTemporals: true,
	}
	want := analyze.Expected{
		Held:             true,
		Parameters:       d.Parameters,
		Results:          d.Results,
		CatalogOwner:     owner,
		CatalogEpoch:     3,
		Temporals:        d.Temporals,
		RefreshTemporals: true,
		PlanOwner:        d.PlanOwner,
		PlanGeneration:   7,
		Types:            d.Types,
		Sequences:        d.Sequences,
		Regtypes:         d.Regtypes.Constants,
	}
	if got := core.ExpectedOf(d); !reflect.DeepEqual(got, want) {
		t.Fatalf("expectedOf = %+v, want %+v", got, want)
	}
	d.Regtypes = nil
	if got := core.ExpectedOf(d); got.Regtypes != nil || !got.Held {
		t.Fatalf("expectedOf without regtypes = %+v", got)
	}
}
