package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/sqltype"
)

// A held description reaches the binder as an analyze value. A missing
// description is not held; a present one copies every input the binder reads.
func TestExpectedOfCopiesHeldInputs(t *testing.T) {
	if got := expectedOf(nil); !reflect.DeepEqual(got, analyze.Expected{}) {
		t.Fatalf("expectedOf(nil) = %+v", got)
	}
	owner := &catalog.Owner{}
	d := &StatementDescription{
		Parameters:       ParameterInfo{Count: 1, Types: []sqltype.Usage{{}}},
		Results:          []catalog.Column{{Name: "a"}},
		catalogOwner:     owner,
		catalogEpoch:     ids.CatalogEpoch(3),
		temporals:        []temporalConstant{{Offset: 4}},
		composites:       map[preparedCompositeKey]compositeOccurrence{{}: {}},
		sequences:        &sequence.Analysis{Constants: []sequence.Constant{{Offset: 5}}},
		regtypes:         &regtypeAnalysis{constants: []regtypeConstant{{Offset: 6}}},
		planOwner:        ids.SessionToken{},
		planGeneration:   7,
		refreshTemporals: true,
	}
	want := analyze.Expected{
		Held:             true,
		Parameters:       d.Parameters,
		Results:          d.Results,
		CatalogOwner:     owner,
		CatalogEpoch:     3,
		Temporals:        d.temporals,
		RefreshTemporals: true,
		PlanOwner:        d.planOwner,
		PlanGeneration:   7,
		Composites:       d.composites,
		Sequences:        d.sequences,
		Regtypes:         d.regtypes.constants,
	}
	if got := expectedOf(d); !reflect.DeepEqual(got, want) {
		t.Fatalf("expectedOf = %+v, want %+v", got, want)
	}
	d.regtypes = nil
	if got := expectedOf(d); got.Regtypes != nil || !got.Held {
		t.Fatalf("expectedOf without regtypes = %+v", got)
	}
}
