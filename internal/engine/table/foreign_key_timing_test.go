package table

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestForeignKeyTimingDefinitionInvariant(t *testing.T) {
	child := &catalog.Relation{Constraints: catalog.RelationConstraints{ForeignKeys: []catalog.ForeignKeyConstraint{{InitiallyDeferred: true}}}}
	if err := ValidateForeignKeys(catalog.Read{}, child, map[string]bool{}); err == nil || err.Error() != "stuntgres: invalid foreign-key timing" {
		t.Fatal("malformed timing accepted", err)
	}
}

func TestForeignKeyImplicitPrimaryProvider(t *testing.T) {
	f := newForeignKeyFixture()
	parent := *f.p
	backing := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 300}
	parent.Constraints.Unique = []catalog.UniqueConstraint{{OID: 400, Index: backing, Primary: true, Attributes: []int32{1}}}
	state := catalog.CloneState(f.view.State())
	state.SetDescriptor(parent.Address(), &parent)
	view := f.view.WithState(state)
	if got := AlterForeignKeyIndex(view, parent.Address(), parser.ForeignKeyConstraint{}); got != backing {
		t.Fatal("implicit primary provider lost exact backing", got)
	}
	changed := parent
	changed.Constraints.Unique = append([]catalog.UniqueConstraint(nil), parent.Constraints.Unique...)
	changed.Constraints.Unique[0].Deferrable = true
	state.SetDescriptor(parent.Address(), &changed)
	if got := AlterForeignKeyIndex(view, parent.Address(), parser.ForeignKeyConstraint{}); got != (ids.ObjectAddress{}) {
		t.Fatal("deferrable primary captured eligible provider", got)
	}
	if parent.Constraints.Unique[0].Deferrable {
		t.Fatal("provider capture mutated source")
	}
}

func TestForeignKeyDeferrableTargetEligibility(t *testing.T) {
	f := newForeignKeyFixture()
	parent := *f.p
	parent.Attributes = []int32{1}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 300}
	deferred := &catalog.Relation{ObjectAddress: address, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Unique: true, Ready: true, Valid: true, Live: true, Keys: []catalog.IndexKey{{Attribute: 1}}}}
	parent.Constraints.Unique = []catalog.UniqueConstraint{{OID: 400, Index: address, Deferrable: true}}
	eligible, err := ForeignKeyIndexEligible(&parent, deferred, []int32{1})
	diagnostic, ok := err.(*sqlstate.Error)
	if eligible || !ok || diagnostic.Code != "55000" || diagnostic.Message != `cannot use a deferrable unique constraint for referenced table "p"` {
		t.Fatal(eligible, err)
	}
	// An unrelated key must not turn a missing matching key into a timing error.
	if eligible, err := ForeignKeyIndexEligible(&parent, deferred, []int32{2}); eligible || err != nil {
		t.Fatal("unrelated key timing affected lookup", eligible, err)
	}
	standalone := *deferred
	standalone.ObjectAddress.ObjectID++
	if eligible, err := ForeignKeyIndexEligible(&parent, &standalone, []int32{1}); !eligible || err != nil {
		t.Fatal("standalone unique index rejected", eligible, err)
	}
	state := catalog.CloneState(f.view.State())
	state.SetDescriptor(parent.Address(), &parent)
	state.SetDescriptor(deferred.Address(), deferred)
	view := f.view.WithState(state)
	if _, err := ForeignKeyIndex(view, &parent, []int32{1}); err == nil {
		t.Fatal("deferrable-only matching key accepted")
	}
	state.SetDescriptor(standalone.Address(), &standalone)
	got, err := ForeignKeyIndex(view, &parent, []int32{1})
	if err != nil || got != &standalone {
		t.Fatal("eligible alternative did not win over deferrable key", got, err)
	}
	if !parent.Constraints.Unique[0].Deferrable {
		t.Fatal("eligibility mutated source definition")
	}
}
