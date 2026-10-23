package regobj

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/sqltype"
)

// A prepared regtype analysis reads the type path only for type, operator
// and routine constants and the collation path only for collation
// references. A missing or invalidated cell is decided without either path.
func TestAnalysisPathsOnDemand(t *testing.T) {
	typeFailure, collationFailure := errors.New("type path unavailable"), errors.New("collation path unavailable")
	failingType := func() (sequence.Environment, error) { return sequence.Environment{}, typeFailure }
	failingCollation := func() ([]ids.ObjectAddress, error) { return nil, collationFailure }
	typePath := func() (sequence.Environment, error) { return sequence.Environment{}, nil }
	collationPath := func() ([]ids.ObjectAddress, error) { return nil, nil }
	var regcollation *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Regcollation {
			regcollation = d
		}
	}
	value, err := sqltype.RegObjectValue(regcollation, 950)
	if err != nil {
		t.Fatal(err)
	}
	collationOnly := []analyze.RegtypeConstant{{TypeID: sqltype.Regcollation, Value: value, References: []analyze.RegObjectReference{{Address: ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950}}}}}
	typeOnly := []analyze.RegtypeConstant{{TypeID: sqltype.Regtype}}

	failing := LookupPaths{Type: failingType, Collation: failingCollation}
	if cell, err := NewAnalysis(Observation{}, failing, nil); cell != nil || err != nil {
		t.Fatal("a statement without reg* constants read a path", cell, err)
	}
	if _, err := NewAnalysis(Observation{}, LookupPaths{Type: failingType, Collation: collationPath}, collationOnly); err != nil {
		t.Fatal("a collation-only constant read the type path", err)
	}
	if _, err := NewAnalysis(Observation{}, LookupPaths{Type: typePath, Collation: failingCollation}, collationOnly); !errors.Is(err, collationFailure) {
		t.Fatal("a collation constant did not read the collation path", err)
	}
	if _, err := NewAnalysis(Observation{}, LookupPaths{Type: typePath, Collation: failingCollation}, typeOnly); err != nil {
		t.Fatal("a type-only constant read the collation path", err)
	}
	if _, err := NewAnalysis(Observation{}, LookupPaths{Type: failingType, Collation: collationPath}, typeOnly); !errors.Is(err, typeFailure) {
		t.Fatal("a type constant did not read the type path", err)
	}

	var missing *Analysis
	if valid, err := missing.Valid(Observation{}, failing); !valid || err != nil {
		t.Fatal("a missing cell is not valid without paths", valid, err)
	}
	cell, err := NewAnalysis(Observation{}, LookupPaths{Type: typePath, Collation: collationPath}, typeOnly)
	if err != nil {
		t.Fatal(err)
	}
	cell.Invalidate()
	if valid, err := cell.Valid(Observation{}, failing); valid || err != nil {
		t.Fatal("an invalidated cell is not invalid without paths", valid, err)
	}
}
