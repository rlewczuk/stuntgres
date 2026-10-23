package catalog

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestCastDefinitionPairIndex(t *testing.T) {
	state, _, _ := catalogReadFixture()
	d := &Cast{OID: 910, ObjectRevision: 1, Source: sqltype.Xid, Target: sqltype.Float4, Context: "e", Method: "b"}
	state.SetDescriptor(d.Address(), d)
	state.BindName(DescriptorNameKey(d), d.Address())
	original := Read{state: state}
	if got, ok := original.Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("missing directed pair")
	}
	if _, ok := original.Cast(d.Target, d.Source); ok {
		t.Fatal("fabricated reverse pair")
	}
	clone := CloneState(state)
	clone.RemoveDescriptor(d.Address())
	if _, ok := (Read{state: clone}).Cast(d.Source, d.Target); ok {
		t.Fatal("removed pair retained")
	}
	if got, ok := original.Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("removal mutated predecessor")
	}
	clone.ResetMaps()
	if _, ok := (Read{state: clone}).Cast(d.Source, d.Target); ok {
		t.Fatal("reset retained pair")
	}
}
func TestCastDefinitionCandidateIsolation(t *testing.T) {
	state, _, _ := catalogReadFixture()
	base := Read{state: state}
	candidate := NewCandidate(base)
	d := &Cast{OID: ids.OID(911), ObjectRevision: 1, Source: sqltype.Xid, Target: sqltype.Float4, Context: "e", Method: "b"}
	if err := candidate.Create(d); err != nil {
		t.Fatal(err)
	}
	if got, ok := candidate.Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("candidate pair missing")
	}
	if _, ok := base.Cast(d.Source, d.Target); ok {
		t.Fatal("candidate published early")
	}
	if got, ok := (Read{state: candidate.State()}).Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("overlay lost pair")
	}
	if err := candidate.Create(&Cast{OID: 912, ObjectRevision: 1, Source: d.Source, Target: d.Target, Context: "i", Method: "i"}); err == nil {
		t.Fatal("duplicate pair accepted")
	}
}

func TestCastDefinitionPrincipalRemap(t *testing.T) {
	state := NewState(1)
	cast := &Cast{
		OID: 910, ObjectRevision: 7, Source: sqltype.Xid, Target: sqltype.Float4,
		Function:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 911},
		InputCast:  ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: 912},
		ResultCast: ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: 913},
		Context:    "e", Method: "f",
	}
	state.SetDescriptor(cast.Address(), cast)
	state.BindName(DescriptorNameKey(cast), cast.Address())
	candidate := CloneState(state)
	unexpected := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		t.Fatal("ownerless cast requested principal authority", ref)
		return authorization.PrincipalRef{}, nil
	}
	if err := RemapPrincipals(candidate, PrincipalRemap{
		Principal: unexpected,
		SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
			return unexpected(ref)
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, view := range []Read{{state: state}, {state: candidate}} {
		got, ok := view.Cast(cast.Source, cast.Target)
		if !ok || got != cast || view.Get(cast.Address()) != cast || view.NameAddress(DescriptorNameKey(cast)) != cast.Address() {
			t.Fatal("principal remap changed ownerless descriptor or object references", got)
		}
	}
}
