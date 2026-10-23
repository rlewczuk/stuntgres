package analyze

import "testing"

func TestDomainIdentityCaptureRetirement(t *testing.T) {
	domain := PreparedTypeKey{Offset: 4, Kind: DomainCast}
	composite := PreparedTypeKey{Offset: 8, Kind: CompositeExpansion}
	typeName := PreparedTypeKey{Offset: 12, Kind: TypeNameCast}
	shared := map[PreparedTypeKey]TypeOccurrence{domain: {Kind: DomainCast}, composite: {Kind: CompositeExpansion}, typeName: {Kind: TypeNameCast}}
	left, retired := RetireDomainCasts(shared)
	if _, ok := left[domain]; ok {
		t.Fatal("domain capture retained after replan")
	}
	if _, ok := left[typeName]; ok {
		t.Fatal("typename capture retained after replan")
	}
	if _, ok := left[composite]; !ok || len(shared) != 3 || !retired {
		t.Fatal("composite/shared captures changed or retirement not reported")
	}
	if again, retired := RetireDomainCasts(left); retired || len(again) != 1 {
		t.Fatal("retiring without casts changed the captures", again, retired)
	}
	left = PublishDomainCasts(left, map[PreparedTypeKey]TypeOccurrence{domain: {Kind: DomainCast}, typeName: {Kind: TypeNameCast}})
	if len(left) != 3 || len(shared) != 3 {
		t.Fatal("successful reanalysis lost unrelated occurrence")
	}
	// Successful candidates replace this cast group without mutating a sibling's
	// captured map. A later failed candidate cannot revive the retired group.
	left, _ = RetireDomainCasts(left)
	left = PublishDomainCasts(left, map[PreparedTypeKey]TypeOccurrence{typeName: {Kind: TypeNameCast}})
	if _, ok := left[domain]; ok || len(left) != 2 || len(shared) != 3 {
		t.Fatal("cast recapture revived a retired occurrence or mutated sibling")
	}
}
