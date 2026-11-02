package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

func TestAmcheckTargetDiagnostics(t *testing.T) {
	e, d, root := amcheckUnitRoot(t, false)
	before := e.Catalog.State
	state := catalog.CloneState(before)
	invalid := *d
	definition := *d.SQLIndex
	definition.Valid = false
	invalid.SQLIndex = &definition
	invalid.ObjectRevision++
	state.SetDescriptor(invalid.Address(), &invalid)
	e.Mu.Lock()
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_parent_check('i',true,true,true)"), sqlstate.Error{Code: "0A000", Message: `cannot check index "i"`, Detail: "Index is not valid."})
	if e.Storage.IndexRoots[d.Address()] != root || len(root.Entries) != 5 {
		t.Fatal("target rejection changed actual index data")
	}
	e.Mu.Lock()
	e.Catalog.PublishLocked(before)
	e.Mu.Unlock()
	if err := amcheckUnitCall(t, e, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
		t.Fatal(err)
	}
}

func TestAmcheckGINTargetDiagnostics(t *testing.T) {
	e, d, root := ginAmcheckUnitRoot(t)
	originalRoot := ginAmcheckClone(root)
	before := e.Catalog.State
	state := catalog.CloneState(before)
	invalid := *d
	definition := *d.SQLIndex
	definition.Valid = false
	invalid.SQLIndex = &definition
	invalid.ObjectRevision = d.Revision() + 1
	state.SetDescriptor(invalid.Address(), &invalid)
	e.Mu.Lock()
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()

	// Publish a real invalid descriptor over the retained GIN root, then enter
	// the installed checker's common checkability path, not CheckGin directly.
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT gin_index_check('i')"), sqlstate.Error{Code: "0A000", Message: `cannot check index "i"`, Detail: "Index is not valid."})
	if e.Storage.IndexRoots[d.Address()] != root || !reflect.DeepEqual(root, originalRoot) || e.Catalog.State != state || state.Get(d.Address()) != &invalid || invalid.SQLIndex.Valid || !d.SQLIndex.Valid {
		t.Fatal("invalid target rejection changed retained descriptor or GIN data")
	}
	e.Mu.Lock()
	e.Catalog.PublishLocked(before)
	e.Mu.Unlock()
	if err := amcheckUnitCall(t, e, "SELECT gin_index_check('i')"); err != nil {
		t.Fatal("healthy target recovery", err)
	}
	if e.Storage.IndexRoots[d.Address()] != root || !reflect.DeepEqual(root, originalRoot) {
		t.Fatal("healthy recovery changed retained GIN data")
	}
}
