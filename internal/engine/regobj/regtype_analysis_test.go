package regobj

import (
	"errors"
	"testing"

	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
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

func TestAnalysisConfigProjection(t *testing.T) {
	var typ *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Regconfig {
			typ = d
		}
	}
	value, err := sqltype.RegTSValue(typ, 90123)
	if err != nil {
		t.Fatal(err)
	}
	state := catalog.NewState(1)
	o := Observation{View: access.View{Catalog: catalog.NewRead(nil, state)}}
	path := sequence.Environment{}
	paths := LookupPaths{Type: func() (sequence.Environment, error) { return path, nil }}
	constants := []analyze.RegtypeConstant{
		{Offset: 11, Operator: sqltype.Regconfig, Value: value},
		{Offset: 22, TypeID: sqltype.Regtype},
	}
	cell, err := NewAnalysis(o, paths, constants)
	if err != nil {
		t.Fatal(err)
	}
	cell.Invalidate()
	projection, err := cell.ForReanalysis(o, paths)
	if err != nil || projection == nil || len(projection.Constants) != 1 || !reflect.DeepEqual(projection.Constants[0], constants[0]) {
		t.Fatal("mixed reanalysis lost original config or retained another alias", projection, err)
	}
	if &projection.Constants[0] == &cell.Constants[0] {
		t.Fatal("candidate shares mutable capture slice")
	}
	// Absence is a current observation, not a fabricated dependency or a
	// reason to replace the captured numeric value.
	accepted, err := NewAnalysis(o, paths, projection.Constants)
	if err != nil || len(accepted.configs) != 1 || accepted.configs[0].present || len(accepted.configs[0].operands) != 0 {
		t.Fatal("missing original invented current dependencies", accepted, err)
	}
	if valid, err := accepted.Valid(o, paths); !valid || err != nil {
		t.Fatal("stable absent original repeatedly invalidates analysis", valid, err)
	}
	// Namespace-only invalidation is independent of effective path equality,
	// and retirement survives a failed candidate followed by restoration.
	state.SetNamespaceGeneration(1)
	if valid, err := accepted.Valid(o, paths); valid || err != nil {
		t.Fatal("namespace generation did not invalidate config", valid, err)
	}
	if projection, err := accepted.ForReanalysis(o, paths); projection != nil || err != nil {
		t.Fatal("namespace mutation retained source capture", projection, err)
	}
	state.SetNamespaceGeneration(0)
	if projection, err := accepted.ForReanalysis(o, paths); projection != nil || err != nil {
		t.Fatal("failed reanalysis revived retired config", projection, err)
	}
}

func TestAnalysisConfigEnvironmentRetirement(t *testing.T) {
	var typ *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Regconfig {
			typ = d
		}
	}
	value, err := sqltype.RegTSValue(typ, 90124)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"database", "epoch", "domain", "path"} {
		t.Run(mutation, func(t *testing.T) {
			state := catalog.NewState(1)
			o := Observation{View: access.View{Catalog: catalog.NewRead(nil, state)}}
			path := sequence.Environment{}
			paths := LookupPaths{Type: func() (sequence.Environment, error) { return path, nil }}
			cell, err := NewAnalysis(o, paths, []analyze.RegtypeConstant{{Operator: sqltype.Regconfig, Value: value}})
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "database":
				o.Database = "another"
			case "epoch":
				state.SetEpoch(2)
			case "domain":
				o.Domain = new(ids.Domain)
			case "path":
				path.ImplicitCatalog = true
			}
			if projection, err := cell.ForReanalysis(o, paths); projection != nil || err != nil {
				t.Fatal("changed resolution environment retained config", projection, err)
			}
			o.Database, o.Domain = "", nil
			state.SetEpoch(1)
			path = sequence.Environment{}
			if projection, err := cell.ForReanalysis(o, paths); projection != nil || err != nil {
				t.Fatal("restored environment revived rejected capture", projection, err)
			}
		})
	}
}
